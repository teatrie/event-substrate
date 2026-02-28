package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/hamba/avro/v2"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/twmb/franz-go/pkg/sr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Package-level structured logger writing to stderr.
var log = zerolog.New(os.Stderr).With().Timestamp().Logger()

type Config struct {
	RedpandaBrokers    string `mapstructure:"REDPANDA_BROKERS"`
	SchemaRegistryURL  string `mapstructure:"SCHEMA_REGISTRY_URL"`
	SupabaseDBURL      string `mapstructure:"SUPABASE_DB_URL"`
	KafkaSASLMechanism string `mapstructure:"KAFKA_SASL_MECHANISM"`
	KafkaSASLUsername  string `mapstructure:"KAFKA_SASL_USERNAME"`
	KafkaSASLPassword  string `mapstructure:"KAFKA_SASL_PASSWORD"`
}

var (
	schemaCache sync.Map
	srClient    *sr.Client
)

func getAvroSchema(ctx context.Context, id int) (avro.Schema, error) {
	if val, ok := schemaCache.Load(id); ok {
		return val.(avro.Schema), nil
	}

	schemaStr, err := srClient.SchemaByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("schema by id %d: %w", id, err)
	}

	parsed, err := avro.Parse(schemaStr.Schema)
	if err != nil {
		return nil, fmt.Errorf("failed to parse avro: %w", err)
	}

	schemaCache.Store(id, parsed)
	return parsed, nil
}

func loadConfig() (*Config, error) {
	viper.SetDefault("REDPANDA_BROKERS", "localhost:9092")
	viper.SetDefault("SCHEMA_REGISTRY_URL", "http://localhost:8081")
	viper.SetDefault("SUPABASE_DB_URL", "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable")
	viper.SetDefault("KAFKA_SASL_MECHANISM", "")
	viper.SetDefault("KAFKA_SASL_USERNAME", "")
	viper.SetDefault("KAFKA_SASL_PASSWORD", "")

	viper.AutomaticEnv()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

type UserMessage struct {
	UserID      string  `avro:"user_id"`
	Email       string  `avro:"email"`
	Message     string  `avro:"message"`
	Timestamp   string  `avro:"timestamp"`
	Visibility  string  `avro:"visibility"`
	RecipientID *string `avro:"recipient_id"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load config")
	}

	topic := "public.user.message.events"
	consumerGroup := "go-message-consumer"

	log.Info().Str("topic", topic).Msg("Starting Message Consumer")

	// OTel initialisation
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	otelShutdown, err := initOTel(ctx, "message-consumer")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialise OTel")
	}
	defer func() {
		if err := otelShutdown(context.Background()); err != nil {
			log.Error().Err(err).Msg("OTel shutdown error")
		}
	}()

	// Kafka metrics
	meter := otel.Meter("message-consumer")
	messagesTotal, err := meter.Int64Counter(
		"kafka.consumer.messages.total",
		metric.WithDescription("Total Kafka messages consumed"),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create messages counter")
	}
	errorsTotal, err := meter.Int64Counter(
		"kafka.consumer.errors.total",
		metric.WithDescription("Total Kafka consumer processing errors"),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create errors counter")
	}

	// DB Setup
	db, err := sql.Open("postgres", cfg.SupabaseDBURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open db")
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		log.Fatal().Err(err).Msg("Failed to ping db")
	}

	// SR Client Setup
	client, err := sr.NewClient(sr.URLs(cfg.SchemaRegistryURL))
	if err != nil {
		log.Fatal().Err(err).Msg("SR client init failed")
	}
	srClient = client

	// Kafka Client Setup
	kgoOpts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(cfg.RedpandaBrokers, ",")...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(consumerGroup),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}
	if cfg.KafkaSASLMechanism != "" && cfg.KafkaSASLUsername != "" {
		switch cfg.KafkaSASLMechanism {
		case "SCRAM-SHA-256":
			kgoOpts = append(kgoOpts, kgo.SASL(scram.Auth{User: cfg.KafkaSASLUsername, Pass: cfg.KafkaSASLPassword}.AsSha256Mechanism()))
			log.Info().Str("user", cfg.KafkaSASLUsername).Msg("SASL/SCRAM-SHA-256 enabled for Kafka")
		case "SCRAM-SHA-512":
			kgoOpts = append(kgoOpts, kgo.SASL(scram.Auth{User: cfg.KafkaSASLUsername, Pass: cfg.KafkaSASLPassword}.AsSha512Mechanism()))
			log.Info().Str("user", cfg.KafkaSASLUsername).Msg("SASL/SCRAM-SHA-512 enabled for Kafka")
		default:
			log.Warn().Str("mechanism", cfg.KafkaSASLMechanism).Msg("Unknown KAFKA_SASL_MECHANISM, connecting without SASL")
		}
	}
	cl, err := kgo.NewClient(kgoOpts...)
	if err != nil {
		log.Fatal().Err(err).Msg("Kafka client init failed")
	}
	defer cl.Close()

	// HTTP server for health endpoints
	mux := http.NewServeMux()
	mux.Handle("/healthz", &healthHandler{})
	mux.Handle("/readyz", &readyzHandler{ping: func() error {
		return db.PingContext(ctx)
	}})

	go func() {
		log.Info().Str("addr", ":8091").Msg("Health HTTP server listening")
		if err := http.ListenAndServe(":8091", mux); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Health HTTP server error")
		}
	}()

	log.Info().Msg("Ready & processing records...")

	for {
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() {
			break
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			log.Error().Interface("errors", errs).Msg("Poll errors")
			continue
		}

		fetches.EachRecord(func(rec *kgo.Record) {
			topicAttr := attribute.String("topic", rec.Topic)

			if len(rec.Value) < 5 {
				log.Warn().Msg("Invalid payload length")
				errorsTotal.Add(ctx, 1, metric.WithAttributes(topicAttr))
				return
			}
			if rec.Value[0] != 0 {
				log.Warn().Msg("Invalid magic byte")
				errorsTotal.Add(ctx, 1, metric.WithAttributes(topicAttr))
				return
			}

			schemaID := int(binary.BigEndian.Uint32(rec.Value[1:5]))
			avroBytes := rec.Value[5:]

			schema, err := getAvroSchema(ctx, schemaID)
			if err != nil {
				log.Error().Err(err).Msg("Schema fetch error")
				errorsTotal.Add(ctx, 1, metric.WithAttributes(topicAttr))
				return
			}

			var msg UserMessage
			if err := avro.Unmarshal(schema, avroBytes, &msg); err != nil {
				log.Error().Err(err).Msg("Unmarshal error")
				errorsTotal.Add(ctx, 1, metric.WithAttributes(topicAttr))
				return
			}

			// Write to unified notifications table (single write target)
			notifPayload, _ := json.Marshal(map[string]string{
				"email":   msg.Email,
				"message": msg.Message,
			})

			visibility := msg.Visibility
			if visibility == "" {
				visibility = "broadcast"
			}

			var recipientID *string
			if msg.RecipientID != nil && *msg.RecipientID != "" {
				recipientID = msg.RecipientID
			}

			notifQuery := `INSERT INTO public.user_notifications (user_id, event_type, payload, event_time, visibility, recipient_id) VALUES ($1, $2, $3, $4, $5, $6)`
			if _, err := db.ExecContext(ctx, notifQuery, msg.UserID, "user.message", string(notifPayload), msg.Timestamp, visibility, recipientID); err != nil {
				log.Fatal().Err(err).Msg("Fatal DB Insert error — crashing to prevent uncommitted message loss")
			}

			messagesTotal.Add(ctx, 1, metric.WithAttributes(
				topicAttr,
				attribute.String("status", "success"),
			))
			log.Info().
				Str("visibility", visibility).
				Str("email", msg.Email).
				Str("user_id", msg.UserID).
				Msg("Inserted message")
		})
	}
}
