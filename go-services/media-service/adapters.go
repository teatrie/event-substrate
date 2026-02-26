package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hamba/avro/v2"
	"github.com/minio/minio-go/v7"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ---------------------------------------------------------------------------
// MinIO adapters
// ---------------------------------------------------------------------------

// minioURLSigner implements both URLSigner (PUT) and DownloadURLSigner (GET).
type minioURLSigner struct {
	client *minio.Client
}

func (m *minioURLSigner) PresignedPutURL(ctx context.Context, bucket, objectKey, _ string, expiry time.Duration) (string, error) {
	u, err := m.client.PresignedPutObject(ctx, bucket, objectKey, expiry)
	if err != nil {
		return "", fmt.Errorf("presigned put: %w", err)
	}
	return u.String(), nil
}

func (m *minioURLSigner) PresignedGetURL(ctx context.Context, bucket, objectKey string, expiry time.Duration) (string, error) {
	u, err := m.client.PresignedGetObject(ctx, bucket, objectKey, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("presigned get: %w", err)
	}
	return u.String(), nil
}

// minioObjectRemover implements ObjectRemover.
type minioObjectRemover struct {
	client *minio.Client
}

func (m *minioObjectRemover) RemoveObject(ctx context.Context, bucket, objectKey string) error {
	return m.client.RemoveObject(ctx, bucket, objectKey, minio.RemoveObjectOptions{})
}

// minioObjectMover implements ObjectMover using CopyObject + RemoveObject.
type minioObjectMover struct {
	client *minio.Client
}

func (m *minioObjectMover) MoveObject(ctx context.Context, bucket, srcKey, dstKey string) error {
	src := minio.CopySrcOptions{Bucket: bucket, Object: srcKey}
	dst := minio.CopyDestOptions{Bucket: bucket, Object: dstKey}
	_, err := m.client.CopyObject(ctx, dst, src)
	if err != nil {
		// Check if source is missing — idempotent if dest already exists
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			// Source gone — check if dest exists (move already completed)
			_, statErr := m.client.StatObject(ctx, bucket, dstKey, minio.StatObjectOptions{})
			if statErr == nil {
				return nil // Dest exists, move already done
			}
		}
		return fmt.Errorf("copy object %s→%s: %w", srcKey, dstKey, err)
	}
	// Remove source after successful copy
	_ = m.client.RemoveObject(ctx, bucket, srcKey, minio.RemoveObjectOptions{})
	return nil
}

// ---------------------------------------------------------------------------
// DB adapters
// ---------------------------------------------------------------------------

// dbFileStore implements FileMetadataLookup and FileDeleter against Postgres.
type dbFileStore struct {
	db *sql.DB
}

func (s *dbFileStore) GetFileMetadata(ctx context.Context, userID, filePath string) (*FileMetadata, error) {
	var meta FileMetadata
	err := s.db.QueryRowContext(ctx,
		`SELECT file_name, media_type, file_size FROM media_files
		 WHERE user_id = $1 AND file_path = $2 AND status = 'active'`,
		userID, filePath,
	).Scan(&meta.FileName, &meta.MediaType, &meta.FileSize)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query file metadata: %w", err)
	}
	return &meta, nil
}

func (s *dbFileStore) SoftDelete(ctx context.Context, userID, filePath string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE media_files SET status = 'deleted' WHERE user_id = $1 AND file_path = $2`,
		userID, filePath,
	)
	if err != nil {
		return fmt.Errorf("soft delete: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Kafka / Avro EventProducer
// ---------------------------------------------------------------------------

// schemaTopicCache caches compiled Avro schemas keyed by topic name (for producing).
var schemaTopicCache sync.Map

type cachedTopicSchema struct {
	id     int
	schema avro.Schema
}

// getSchemaForTopic fetches and caches the latest schema for a topic's `-value` subject.
func getSchemaForTopic(ctx context.Context, topic string) (*cachedTopicSchema, error) {
	if val, ok := schemaTopicCache.Load(topic); ok {
		return val.(*cachedTopicSchema), nil
	}

	subject := fmt.Sprintf("%s-value", topic)
	schemaSubject, err := srClient.SchemaByVersion(ctx, subject, -1)
	if err != nil {
		return nil, fmt.Errorf("fetch schema for subject %q: %w", subject, err)
	}

	parsed, err := avro.Parse(schemaSubject.Schema.Schema)
	if err != nil {
		return nil, fmt.Errorf("parse avro schema for topic %q: %w", topic, err)
	}

	cached := &cachedTopicSchema{id: schemaSubject.ID, schema: parsed}
	schemaTopicCache.Store(topic, cached)
	log.Printf("Cached producer schema ID %d for topic %q", cached.id, topic)
	return cached, nil
}

// avroKafkaProducer implements EventProducer by encoding events as Avro and
// producing synchronously to Kafka with the Confluent wire format header.
type avroKafkaProducer struct {
	client *kgo.Client
}

func (p *avroKafkaProducer) Produce(ctx context.Context, topic string, event map[string]any) error {
	cached, err := getSchemaForTopic(ctx, topic)
	if err != nil {
		return fmt.Errorf("schema for topic %q: %w", topic, err)
	}

	avroBytes, err := avro.Marshal(cached.schema, event)
	if err != nil {
		return fmt.Errorf("avro marshal for topic %q: %w", topic, err)
	}

	// Confluent Wire Format: magic byte (0x00) + 4-byte schema ID + avro payload.
	var buf bytes.Buffer
	buf.WriteByte(0)
	idBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(idBytes, uint32(cached.id))
	buf.Write(idBytes)
	buf.Write(avroBytes)

	if err := p.client.ProduceSync(ctx, &kgo.Record{Topic: topic, Value: buf.Bytes()}).FirstErr(); err != nil {
		return fmt.Errorf("kafka produce to %q: %w", topic, err)
	}
	return nil
}
