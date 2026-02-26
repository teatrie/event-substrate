package main

import (
	"fmt"

	"github.com/spf13/viper"
)

// Config holds all media-service configuration, bound from environment variables.
type Config struct {
	RedpandaBrokers     string `mapstructure:"REDPANDA_BROKERS"`
	SchemaRegistryURL   string `mapstructure:"SCHEMA_REGISTRY_URL"`
	SupabaseDBURL       string `mapstructure:"SUPABASE_DB_URL"`
	MinioEndpoint       string `mapstructure:"MINIO_ENDPOINT"`
	MinioPublicEndpoint string `mapstructure:"MINIO_PUBLIC_ENDPOINT"`
	MinioAccessKey      string `mapstructure:"MINIO_ACCESS_KEY"`
	MinioSecretKey      string `mapstructure:"MINIO_SECRET_KEY"`
	MinioBucket         string `mapstructure:"MINIO_BUCKET"`
	KafkaSASLMechanism  string `mapstructure:"KAFKA_SASL_MECHANISM"`
	KafkaSASLUsername   string `mapstructure:"KAFKA_SASL_USERNAME"`
	KafkaSASLPassword   string `mapstructure:"KAFKA_SASL_PASSWORD"`
	UploadURLExpiry     int    `mapstructure:"UPLOAD_URL_EXPIRY"`
	DownloadURLExpiry   int    `mapstructure:"DOWNLOAD_URL_EXPIRY"`
}

func loadConfig() (*Config, error) {
	viper.SetDefault("REDPANDA_BROKERS", "localhost:9092")
	viper.SetDefault("SCHEMA_REGISTRY_URL", "http://localhost:8081")
	viper.SetDefault("SUPABASE_DB_URL", "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable")
	viper.SetDefault("MINIO_ENDPOINT", "localhost:9000")
	viper.SetDefault("MINIO_PUBLIC_ENDPOINT", "localhost:9000")
	viper.SetDefault("MINIO_ACCESS_KEY", "admin")
	viper.SetDefault("MINIO_SECRET_KEY", "password")
	viper.SetDefault("MINIO_BUCKET", "media-uploads")
	viper.SetDefault("KAFKA_SASL_MECHANISM", "")
	viper.SetDefault("KAFKA_SASL_USERNAME", "")
	viper.SetDefault("KAFKA_SASL_PASSWORD", "")
	viper.SetDefault("UPLOAD_URL_EXPIRY", 900)
	viper.SetDefault("DOWNLOAD_URL_EXPIRY", 3600)

	viper.AutomaticEnv()

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}
