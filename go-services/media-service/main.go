package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/hamba/avro/v2"
	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/twmb/franz-go/pkg/sr"
)

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
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	log.Printf("Starting Media Service. Topics: %v", consumeTopics)

	// DB Setup
	db, err := sql.Open("postgres", cfg.SupabaseDBURL)
	if err != nil {
		log.Fatal("Failed to open db:", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)

	// MinIO Client Setup
	minioClient, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: false,
		Region: "us-east-1",
	})
	if err != nil {
		log.Fatal("MinIO client:", err)
	}

	// Public MinIO client for generating browser-reachable presigned URLs
	publicMinioClient, err := minio.New(cfg.MinioPublicEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: false,
		Region: "us-east-1",
	})
	if err != nil {
		log.Fatal("Public MinIO client:", err)
	}

	// SR Client Setup
	client, err := sr.NewClient(sr.URLs(cfg.SchemaRegistryURL))
	if err != nil {
		log.Fatal("SR client:", err)
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
		log.Fatal("Kafka producer:", err)
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
		log.Printf("SASL/SCRAM-SHA-256 enabled for Kafka user '%s'", cfg.KafkaSASLUsername)
	}
	consumer, err := kgo.NewClient(consumerOpts...)
	if err != nil {
		log.Fatal("Kafka consumer:", err)
	}
	defer consumer.Close()

	// Adapters
	publicSigner := &minioURLSigner{client: publicMinioClient}
	objectRemover := &minioObjectRemover{client: minioClient}
	fileStore := &dbFileStore{db: db}
	eventProducer := &avroKafkaProducer{client: producer}
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
	})

	// HTTP server for MinIO webhook callbacks
	uploadWebhook := NewUploadWebhookHandler(eventProducer, objectMover, cfg.MinioBucket)
	fileReadyWebhook := NewFileReadyWebhookHandler(eventProducer)

	webhookMux := http.NewServeMux()
	webhookMux.Handle("/webhooks/media-upload", uploadWebhook)
	webhookMux.Handle("/webhooks/file-ready", fileReadyWebhook)

	webhookServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.WebhookPort),
		Handler: webhookMux,
	}

	go func() {
		log.Printf("Webhook HTTP server listening on :%d", cfg.WebhookPort)
		if err := webhookServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Webhook server error: %v", err)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	log.Println("Ready & processing records...")

	for {
		fetches := consumer.PollFetches(ctx)
		if fetches.IsClientClosed() {
			break
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			log.Println("Poll errors:", errs)
			continue
		}

		fetches.EachRecord(func(rec *kgo.Record) {
			if len(rec.Value) < 5 || rec.Value[0] != 0 {
				log.Printf("Invalid payload on topic %s", rec.Topic)
				return
			}

			schemaID := int(binary.BigEndian.Uint32(rec.Value[1:5]))
			avroBytes := rec.Value[5:]

			schema, err := getAvroSchema(ctx, schemaID)
			if err != nil {
				log.Printf("Schema fetch err for topic %s: %v", rec.Topic, err)
				return
			}

			var payload map[string]any
			if err := avro.Unmarshal(schema, avroBytes, &payload); err != nil {
				log.Printf("Unmarshal err for topic %s: %v", rec.Topic, err)
				return
			}

			router.Route(ctx, rec.Topic, payload)
		})
	}
}
