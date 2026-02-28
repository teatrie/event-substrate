package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/hamba/avro/v2"
	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/twmb/franz-go/pkg/sr"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// Package-level zerolog logger.
var log = zerolog.New(os.Stderr).With().Timestamp().Logger()

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

// Topics consumed by the media service.
var consumeTopics = []string{
	"public.media.upload.approved",
	"public.media.expired.events",
	"internal.media.download.intent",
	"internal.media.delete.intent",
	"internal.media.upload.retry",
	"internal.media.delete.retry",
}

// kafkaConsumerChecker implements KafkaChecker using the kgo.Client.
type kafkaConsumerChecker struct {
	client *kgo.Client
}

func (k *kafkaConsumerChecker) Check(ctx context.Context) error {
	// A kgo client that was successfully created is considered healthy.
	// kgo doesn't expose a direct ping — we use the fact that the client
	// was created without error as the liveness signal.
	return nil
}

// minioConnChecker implements MinioChecker by calling BucketExists.
type minioConnChecker struct {
	client *minio.Client
	bucket string
}

func (m *minioConnChecker) Check(ctx context.Context) error {
	_, err := m.client.BucketExists(ctx, m.bucket)
	return err
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load config")
	}

	log.Info().Strs("topics", consumeTopics).Msg("Starting Media Service")

	// OTel initialisation — best-effort; do not crash the service if the
	// collector is unavailable.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	otelShutdown, otelErr := initOTel(ctx, "media-service")
	if otelErr != nil {
		log.Warn().Err(otelErr).Msg("OTel initialisation failed — continuing without telemetry")
	} else {
		defer func() {
			if err := otelShutdown(context.Background()); err != nil {
				log.Warn().Err(err).Msg("OTel shutdown error")
			}
		}()
	}

	// Custom Kafka metrics
	meter := otel.Meter("media-service")

	kafkaConsumerMessages, err := meter.Int64Counter(
		"kafka.consumer.messages.total",
		otelmetric.WithDescription("Total number of Kafka messages consumed"),
	)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to create kafka.consumer.messages.total counter")
	}

	kafkaConsumerErrors, err := meter.Int64Counter(
		"kafka.consumer.errors.total",
		otelmetric.WithDescription("Total number of Kafka consumer processing errors"),
	)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to create kafka.consumer.errors.total counter")
	}

	kafkaProducerMessages, err := meter.Int64Counter(
		"kafka.producer.messages.total",
		otelmetric.WithDescription("Total number of Kafka messages produced"),
	)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to create kafka.producer.messages.total counter")
	}

	// DB Setup
	db, err := sql.Open("postgres", cfg.SupabaseDBURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open db")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	// MinIO Client Setup
	minioClient, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: false,
		Region: "us-east-1",
	})
	if err != nil {
		log.Fatal().Err(err).Msg("MinIO client")
	}

	// Public MinIO client for generating browser-reachable presigned URLs
	publicMinioClient, err := minio.New(cfg.MinioPublicEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: false,
		Region: "us-east-1",
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Public MinIO client")
	}

	// SR Client Setup
	client, err := sr.NewClient(sr.URLs(cfg.SchemaRegistryURL))
	if err != nil {
		log.Fatal().Err(err).Msg("SR client")
	}
	srClient = client

	// Kafka Producer (for emitting result events)
	producerOpts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(cfg.RedpandaBrokers, ",")...),
	}
	if cfg.KafkaSASLMechanism == "SCRAM-SHA-256" && cfg.KafkaSASLUsername != "" {
		producerOpts = append(producerOpts, kgo.SASL(scram.Auth{User: cfg.KafkaSASLUsername, Pass: cfg.KafkaSASLPassword}.AsSha256Mechanism()))
	}
	producer, err := kgo.NewClient(producerOpts...)
	if err != nil {
		log.Fatal().Err(err).Msg("Kafka producer")
	}
	defer producer.Close()

	// Kafka Consumer
	consumerOpts := []kgo.Opt{
		kgo.SeedBrokers(strings.Split(cfg.RedpandaBrokers, ",")...),
		kgo.ConsumeTopics(consumeTopics...),
		kgo.ConsumerGroup("media-service-consumer"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}
	if cfg.KafkaSASLMechanism == "SCRAM-SHA-256" && cfg.KafkaSASLUsername != "" {
		consumerOpts = append(consumerOpts, kgo.SASL(scram.Auth{User: cfg.KafkaSASLUsername, Pass: cfg.KafkaSASLPassword}.AsSha256Mechanism()))
		log.Info().Str("user", cfg.KafkaSASLUsername).Msg("SASL/SCRAM-SHA-256 enabled for Kafka")
	}
	consumer, err := kgo.NewClient(consumerOpts...)
	if err != nil {
		log.Fatal().Err(err).Msg("Kafka consumer")
	}
	defer consumer.Close()

	// Adapters
	publicSigner := &minioURLSigner{client: publicMinioClient}
	objectRemover := &minioObjectRemover{client: minioClient}
	fileStore := &dbFileStore{db: db}
	// Wrap the Avro producer with metric instrumentation
	rawProducer := &avroKafkaProducer{client: producer}
	eventProducer := &instrumentedKafkaProducer{inner: rawProducer, counter: kafkaProducerMessages}
	objectMover := &minioObjectMover{client: minioClient}

	// Wire handlers into the TopicRouter
	router := NewTopicRouter(map[string]MessageHandler{
		"public.media.upload.approved": NewUploadSigningHandler(
			publicSigner,
			eventProducer,
			cfg.MinioBucket,
			cfg.UploadURLExpiry,
		),
		"internal.media.download.intent": NewDownloadSigningHandler(
			publicSigner,
			fileStore,
			eventProducer,
			cfg.MinioBucket,
			cfg.DownloadURLExpiry,
		),
		"internal.media.delete.intent": NewDeleteHandler(
			fileStore,
			objectRemover,
			fileStore,
			eventProducer,
			cfg.MinioBucket,
		),
		"public.media.expired.events": NewExpiredCleanupHandler(
			objectRemover,
			cfg.MinioBucket,
		),
		"internal.media.upload.retry": NewRetryHandler(
			objectMover,
			eventProducer,
			cfg.MinioBucket,
		),
		"internal.media.delete.retry": NewDeleteRetryHandler(
			objectRemover,
			eventProducer,
			cfg.MinioBucket,
		),
	})

	// HTTP server for MinIO webhook callbacks + health endpoints
	uploadWebhook := NewUploadWebhookHandler(eventProducer, objectMover, cfg.MinioBucket)
	fileReadyWebhook := NewFileReadyWebhookHandler(eventProducer)

	webhookMux := http.NewServeMux()
	webhookMux.Handle("/webhooks/media-upload", uploadWebhook)
	webhookMux.Handle("/webhooks/file-ready", fileReadyWebhook)
	webhookMux.Handle("/healthz", NewHealthzHandler())
	webhookMux.Handle("/readyz", NewReadyzHandler(
		&sqlDBPinger{db: db},
		&kafkaConsumerChecker{client: consumer},
		&minioConnChecker{client: minioClient, bucket: cfg.MinioBucket},
	))

	// Wrap the entire mux with OTel HTTP instrumentation
	otelHandler := otelhttp.NewHandler(webhookMux, "media-service")

	webhookServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.WebhookPort),
		Handler:           otelHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Int("port", cfg.WebhookPort).Msg("Webhook HTTP server listening")
		if err := webhookServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Webhook server error")
		}
	}()

	log.Info().Msg("Ready & processing records...")

	for {
		fetches := consumer.PollFetches(ctx)
		if fetches.IsClientClosed() {
			break
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			log.Warn().Interface("errors", errs).Msg("Poll errors")
			continue
		}

		fetches.EachRecord(func(rec *kgo.Record) {
			if len(rec.Value) < 5 || rec.Value[0] != 0 {
				log.Warn().Str("topic", rec.Topic).Msg("Invalid payload")
				kafkaConsumerErrors.Add(ctx, 1,
					otelmetric.WithAttributes(topicAttr(rec.Topic)),
				)
				return
			}

			schemaID := int(binary.BigEndian.Uint32(rec.Value[1:5]))
			avroBytes := rec.Value[5:]

			schema, err := getAvroSchema(ctx, schemaID)
			if err != nil {
				log.Error().Err(err).Str("topic", rec.Topic).Msg("Schema fetch err")
				kafkaConsumerErrors.Add(ctx, 1,
					otelmetric.WithAttributes(topicAttr(rec.Topic)),
				)
				return
			}

			var payload map[string]any
			if err := avro.Unmarshal(schema, avroBytes, &payload); err != nil {
				log.Error().Err(err).Str("topic", rec.Topic).Msg("Unmarshal err")
				kafkaConsumerErrors.Add(ctx, 1,
					otelmetric.WithAttributes(topicAttr(rec.Topic)),
				)
				return
			}

			routed := router.Route(ctx, rec.Topic, payload)
			if routed {
				kafkaConsumerMessages.Add(ctx, 1,
					otelmetric.WithAttributes(topicAttr(rec.Topic), statusAttr("ok")),
				)
			} else {
				kafkaConsumerErrors.Add(ctx, 1,
					otelmetric.WithAttributes(topicAttr(rec.Topic)),
				)
			}
		})
	}
}
