package main

import (
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"RedpandaBrokers", cfg.RedpandaBrokers, "localhost:9092"},
		{"SchemaRegistryURL", cfg.SchemaRegistryURL, "http://localhost:8081"},
		{"MinioEndpoint", cfg.MinioEndpoint, "localhost:9000"},
		{"MinioPublicEndpoint", cfg.MinioPublicEndpoint, "localhost:9000"},
		{"MinioAccessKey", cfg.MinioAccessKey, "admin"},
		{"MinioSecretKey", cfg.MinioSecretKey, "password"},
		{"MinioBucket", cfg.MinioBucket, "media-uploads"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %q, want %q", tt.got, tt.expected)
			}
		})
	}
}

func TestLoadConfig_UploadURLExpiryDefault(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}
	if cfg.UploadURLExpiry != 900 {
		t.Errorf("UploadURLExpiry = %d, want 900", cfg.UploadURLExpiry)
	}
}

func TestLoadConfig_DownloadURLExpiryDefault(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}
	if cfg.DownloadURLExpiry != 3600 {
		t.Errorf("DownloadURLExpiry = %d, want 3600", cfg.DownloadURLExpiry)
	}
}

func TestLoadConfig_DBURLDefault(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}
	expected := "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable"
	if cfg.SupabaseDBURL != expected {
		t.Errorf("SupabaseDBURL = %q, want %q", cfg.SupabaseDBURL, expected)
	}
}
