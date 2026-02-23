package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/hamba/avro/v2"
	_ "github.com/lib/pq"
	"github.com/spf13/viper"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/twmb/franz-go/pkg/sr"
)

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
		log.Fatal("Failed to load config:", err)
	}

	topic := "public.user.message.events"
	consumerGroup := "go-message-consumer"

	log.Printf("Starting Message Consumer. Topic: %s", topic)

	// DB Setup
	db, err := sql.Open("postgres", cfg.SupabaseDBURL)
	if err != nil {
		log.Fatal("Failed to open db:", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal("Failed to ping db:", err)
	}

	// SR Client Setup
	client, err := sr.NewClient(sr.URLs(cfg.SchemaRegistryURL))
	if err != nil {
		log.Fatal("SR client:", err)
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
			log.Printf("SASL/SCRAM-SHA-256 enabled for Kafka user '%s'", cfg.KafkaSASLUsername)
		case "SCRAM-SHA-512":
			kgoOpts = append(kgoOpts, kgo.SASL(scram.Auth{User: cfg.KafkaSASLUsername, Pass: cfg.KafkaSASLPassword}.AsSha512Mechanism()))
			log.Printf("SASL/SCRAM-SHA-512 enabled for Kafka user '%s'", cfg.KafkaSASLUsername)
		default:
			log.Printf("WARNING: Unknown KAFKA_SASL_MECHANISM '%s', connecting without SASL", cfg.KafkaSASLMechanism)
		}
	}
	cl, err := kgo.NewClient(kgoOpts...)
	if err != nil {
		log.Fatal("Kafka client:", err)
	}
	defer cl.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Println("Ready & processing records...")

	for {
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() {
			break
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			log.Println("Poll errors:", errs)
			continue
		}

		fetches.EachRecord(func(rec *kgo.Record) {
			if len(rec.Value) < 5 {
				log.Println("Invalid payload length")
				return
			}
			if rec.Value[0] != 0 {
				log.Println("Invalid magic byte")
				return
			}

			schemaID := int(binary.BigEndian.Uint32(rec.Value[1:5]))
			avroBytes := rec.Value[5:]

			schema, err := getAvroSchema(ctx, schemaID)
			if err != nil {
				log.Printf("Schema fetch err: %v", err)
				return
			}

			var msg UserMessage
			if err := avro.Unmarshal(schema, avroBytes, &msg); err != nil {
				log.Printf("Unmarshal err: %v", err)
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
				log.Fatalf("Fatal DB Insert error: %v. Crashing to prevent uncommitted message loss.", err)
			}

			log.Printf("Inserted %s message from %s (UID: %s)", visibility, msg.Email, msg.UserID)
		})
	}
}
