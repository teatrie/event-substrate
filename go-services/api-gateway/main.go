package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/golang-jwt/jwt/v5"
	"github.com/hamba/avro/v2"
	"github.com/spf13/viper"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/twmb/franz-go/pkg/sr"
)

// CachedSchema holds the parsed schema ID and compiled Avro layout to prevent repetitive HTTP lookups
type CachedSchema struct {
	ID     int
	Schema avro.Schema
}

type Config struct {
	WebhookRoutes  map[string]string `mapstructure:"webhookRoutes"`
	ExternalRoutes map[string]string `mapstructure:"externalRoutes"`
}

var (
	appConfig      Config
	configMu       sync.RWMutex
	schemaCache    sync.Map
	srClient       *sr.Client
	kafkaClient    *kgo.Client
	jwtSecret      []byte
	ecdsaPublicKey *ecdsa.PublicKey
	webhookSecret  string
)

// fetchJWKS fetches the JWKS from a Supabase auth endpoint and extracts the EC public key.
func fetchJWKS(jwksURL string) (*ecdsa.PublicKey, error) {
	resp, err := http.Get(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
			Alg string `json:"alg"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("failed to decode JWKS: %w", err)
	}

	for _, key := range jwks.Keys {
		if key.Kty == "EC" && key.Crv == "P-256" {
			xBytes, err := base64.RawURLEncoding.DecodeString(key.X)
			if err != nil {
				return nil, fmt.Errorf("failed to decode JWKS x coordinate: %w", err)
			}
			yBytes, err := base64.RawURLEncoding.DecodeString(key.Y)
			if err != nil {
				return nil, fmt.Errorf("failed to decode JWKS y coordinate: %w", err)
			}
			return &ecdsa.PublicKey{
				Curve: elliptic.P256(),
				X:     new(big.Int).SetBytes(xBytes),
				Y:     new(big.Int).SetBytes(yBytes),
			}, nil
		}
	}
	return nil, fmt.Errorf("no EC P-256 key found in JWKS")
}

// jwtKeyFunc returns the appropriate verification key based on the token's signing method.
// Supports both HS256 (HMAC) and ES256 (ECDSA) to handle varying Supabase JWT configurations.
func jwtKeyFunc(token *jwt.Token) (interface{}, error) {
	switch token.Method.(type) {
	case *jwt.SigningMethodHMAC:
		if len(jwtSecret) == 0 {
			return nil, fmt.Errorf("HMAC JWT secret not configured")
		}
		return jwtSecret, nil
	case *jwt.SigningMethodECDSA:
		if ecdsaPublicKey == nil {
			return nil, fmt.Errorf("ECDSA public key not configured")
		}
		return ecdsaPublicKey, nil
	default:
		return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
	}
}

func loadConfig() {
	viper.SetConfigName("routes")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/app") // Kubernetes/Docker volume mount
	viper.AddConfigPath(".")    // Local fallback

	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Warning: Could not read config file: %v", err)
	} else {
		updateConfig()
	}

	viper.OnConfigChange(func(e fsnotify.Event) {
		log.Println("Config file changed dynamically via fsnotify:", e.Name)
		updateConfig()
	})
	viper.WatchConfig()
}

func updateConfig() {
	var newConfig Config
	if err := viper.Unmarshal(&newConfig); err != nil {
		log.Printf("Unable to decode routes.yaml into Config struct: %v", err)
		return
	}
	configMu.Lock()
	appConfig = newConfig
	configMu.Unlock()
	log.Printf("Loaded Zero-Downtime Routes Config: Webhook=%d allowed topics, External=%d allowed topics", len(newConfig.WebhookRoutes), len(newConfig.ExternalRoutes))
}

func fetchAndCacheSchema(ctx context.Context, topic string) (*CachedSchema, error) {
	// First, check the local sync.Map cache radially
	subject := fmt.Sprintf("%s-value", topic)
	if val, ok := schemaCache.Load(subject); ok {
		return val.(*CachedSchema), nil
	}

	// Cache miss: Execute HTTP request to Confluent Schema Registry
	log.Printf("Schema not found locally for topic '%s'. Fetching from registry...", topic)
	schemaSubject, err := srClient.SchemaByVersion(ctx, subject, -1)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest schema for subject '%s': %w", subject, err)
	}

	// Compile the raw schema string into an executable hamba/avro Schema
	avroSchema, err := avro.Parse(schemaSubject.Schema.Schema)
	if err != nil {
		return nil, fmt.Errorf("failed to parse retrieved schema string into compiled Avro format: %w", err)
	}

	// Cache the compiled structure
	cached := &CachedSchema{
		ID:     schemaSubject.ID,
		Schema: avroSchema,
	}
	schemaCache.Store(subject, cached)
	log.Printf("Successfully cached Schema ID %d for topic '%s'", cached.ID, topic)

	return cached, nil
}

// convertJSONNumbers recursively converts json.Number values to native Go int64/float64
// for compatibility with hamba/avro, which requires native types for Avro encoding.
func convertJSONNumbers(m map[string]any) {
	for k, v := range m {
		switch val := v.(type) {
		case json.Number:
			// Try int64 first (for Avro "int" and "long" fields), fall back to float64
			if i, err := val.Int64(); err == nil {
				m[k] = i
			} else if f, err := val.Float64(); err == nil {
				m[k] = f
			}
		case map[string]any:
			convertJSONNumbers(val)
		case []any:
			for _, item := range val {
				if nested, ok := item.(map[string]any); ok {
					convertJSONNumbers(nested)
				}
			}
		}
	}
}

// encodeAvro dynamically serializes an unstructured map[string]any against the retrieved Avro Schema structure, prepending the 5-byte Confluent Wire Format headers.
func encodeAvro(schemaID int, avroSchema avro.Schema, v any) ([]byte, error) {
	avroBytes, err := avro.Marshal(avroSchema, v)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteByte(0) // Confluent Wire Format Magic Byte

	idBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(idBytes, uint32(schemaID))
	buf.Write(idBytes)

	buf.Write(avroBytes)
	return buf.Bytes(), nil
}

func processAndProduceEvent(ctx context.Context, w http.ResponseWriter, r *http.Request, topicName string) {
	// Limit request body size to 1MB to prevent OOM exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Error reading request body", http.StatusInternalServerError)
		}
		return
	}

	// Attempt to deserialize the raw incoming Request directly into an unstructured map.
	// UseNumber() preserves numeric types as json.Number instead of float64,
	// which we then convert to native Go int64/float64 for the Avro encoder.
	var event map[string]any
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		log.Printf("Invalid JSON formatting for topic '%s': %v", topicName, err)
		http.Error(w, "Invalid JSON structure", http.StatusBadRequest)
		return
	}

	// Convert json.Number values to native Go types for the Avro encoder.
	// hamba/avro expects int64 for "long" and float64 for "double", not json.Number.
	convertJSONNumbers(event)

	// Attempt to resolve the targeted Avro schema dynamically from memory cache or Confluent DB
	cachedSchema, err := fetchAndCacheSchema(ctx, topicName)
	if err != nil {
		log.Printf("Schema Resolution Failed for %s: %v", topicName, err)
		http.Error(w, fmt.Sprintf("Failed to resolve schema registry formatting for topic: %s", topicName), http.StatusBadRequest)
		return
	}

	// Serialize the generic map[string]any using the fetched Avro definition map
	avroPayload, err := encodeAvro(cachedSchema.ID, cachedSchema.Schema, event)
	if err != nil {
		log.Printf("Failed to encode payload against dynamic avro map for %s: %v", topicName, err)
		http.Error(w, "Internal schema formatting violation", http.StatusBadRequest)
		return
	}

	// Send to Redpanda asynchronously under the structurally derived topic
	record := &kgo.Record{Topic: topicName, Value: avroPayload}
	// Use async producing (Promise) rather than ProduceSync per HTTP request for performance
	kafkaClient.Produce(context.Background(), record, func(_ *kgo.Record, err error) {
		if err != nil {
			log.Printf("Failed to produce async record to redpanda cluster topic '%s': %v", topicName, err)
		}
	})

	w.WriteHeader(http.StatusNoContent)
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Authenticate the static webhook secret generated by Supabase pg_net
	providedSecret := r.Header.Get("X-Webhook-Secret")
	if webhookSecret != "" && providedSecret != webhookSecret {
		http.Error(w, "Unauthorized Webhook Connection", http.StatusUnauthorized)
		return
	}

	// 2. Map route mapping from URL (e.g. /webhooks/login)
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[0] != "webhooks" {
		http.Error(w, "Invalid endpoint routing", http.StatusBadRequest)
		return
	}
	topicKey := strings.ToLower(pathParts[1])

	// 3. Strict Allowlisting evaluation protecting against topology poisoning
	configMu.RLock()
	topicName, exists := appConfig.WebhookRoutes[topicKey]
	configMu.RUnlock()

	if !exists {
		log.Printf("Blocked unauthorized webhook attempt to target missing topic key: %s", topicKey)
		http.Error(w, "Forbidden (Topic mapping not found in allowlist)", http.StatusForbidden)
		return
	}

	processAndProduceEvent(r.Context(), w, r, topicName)
}

func externalHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Authenticate External Frontend Client via Supabase JWT Bearer Token
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Unauthorized (Missing or invalid Bearer token)", http.StatusUnauthorized)
		return
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	// Note: We skip complex standard claims validation here and just verify signature integrity for speed, but full validation can be expanded.
	token, err := jwt.Parse(tokenString, jwtKeyFunc)

	if len(jwtSecret) > 0 || ecdsaPublicKey != nil {
		if err != nil || !token.Valid {
			http.Error(w, "Unauthorized (Invalid JWT Signature)", http.StatusUnauthorized)
			return
		}
	} else {
		log.Println("WARNING: No JWT verification key configured. Bypassing token signature validation.")
	}

	// 2. Map route mapping from URL (e.g. /api/v1/events/click)
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 || pathParts[0] != "api" || pathParts[1] != "v1" || pathParts[2] != "events" {
		http.Error(w, "Invalid endpoint routing format", http.StatusBadRequest)
		return
	}
	topicKey := strings.ToLower(pathParts[3])

	// 3. Strict Allowlisting evaluation protecting against topology poisoning from external actors
	configMu.RLock()
	topicName, exists := appConfig.ExternalRoutes[topicKey]
	configMu.RUnlock()

	if !exists {
		log.Printf("Blocked unauthorized external client attempt to target missing topic key: %s", topicKey)
		http.Error(w, "Forbidden (Topic mapping not found in allowlist)", http.StatusForbidden)
		return
	}

	processAndProduceEvent(r.Context(), w, r, topicName)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-Webhook-Secret")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	var err error

	// Load Infrastructure Variables
	brokersEnv := os.Getenv("REDPANDA_BROKERS")
	if brokersEnv == "" {
		brokersEnv = "host.docker.internal:9092"
	}
	brokers := []string{brokersEnv}

	srEnv := os.Getenv("SCHEMA_REGISTRY_URL")
	if srEnv == "" {
		srEnv = "http://host.docker.internal:8081"
	}

	// Load Security Secrets Minimum Parameters
	jwtSecretEnv := os.Getenv("SUPABASE_JWT_SECRET")
	if jwtSecretEnv != "" {
		jwtSecret = []byte(jwtSecretEnv)
	}

	// Fetch ECDSA public key from Supabase JWKS endpoint for ES256 JWT validation
	jwksURL := os.Getenv("SUPABASE_JWKS_URL")
	if jwksURL != "" {
		pubKey, err := fetchJWKS(jwksURL)
		if err != nil {
			log.Printf("WARNING: Failed to fetch JWKS from %s: %v", jwksURL, err)
		} else {
			ecdsaPublicKey = pubKey
			log.Printf("ECDSA (ES256) JWT verification enabled via JWKS from %s", jwksURL)
		}
	}

	webhookSecret = os.Getenv("WEBHOOK_SECRET")
	if webhookSecret == "" {
		log.Println("WARNING: WEBHOOK_SECRET is not set. Internal webhooks are missing token validation defense.")
	}

	// Load SASL Configuration
	saslMechanism := os.Getenv("KAFKA_SASL_MECHANISM")
	saslUser := os.Getenv("KAFKA_SASL_USERNAME")
	saslPass := os.Getenv("KAFKA_SASL_PASSWORD")

	log.Println("Starting Secure Dual-Ingress Go API Gateway...")

	// 0. Bootstrap Hot-Reloading Configuration
	loadConfig()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// 1. Setup global Kafka Client for Produce
	kgoOpts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
	}
	if saslMechanism != "" && saslUser != "" {
		switch saslMechanism {
		case "SCRAM-SHA-256":
			kgoOpts = append(kgoOpts, kgo.SASL(scram.Auth{User: saslUser, Pass: saslPass}.AsSha256Mechanism()))
			log.Printf("SASL/SCRAM-SHA-256 enabled for Kafka user '%s'", saslUser)
		case "SCRAM-SHA-512":
			kgoOpts = append(kgoOpts, kgo.SASL(scram.Auth{User: saslUser, Pass: saslPass}.AsSha512Mechanism()))
			log.Printf("SASL/SCRAM-SHA-512 enabled for Kafka user '%s'", saslUser)
		default:
			log.Printf("WARNING: Unknown KAFKA_SASL_MECHANISM '%s', connecting without SASL", saslMechanism)
		}
	}
	kafkaClient, err = kgo.NewClient(kgoOpts...)
	if err != nil {
		log.Fatalf("unable to create kafka client: %v", err)
	}
	defer kafkaClient.Close()

	// 2. Setup global Schema Registry Client
	srClient, err = sr.NewClient(sr.URLs(srEnv))
	if err != nil {
		log.Fatalf("unable to create schema registry client: %v", err)
	}

	// 3. HTTP Server configuration - Splitting branches cleanly for strict access matrices
	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/media-upload", mediaWebhookHandler)
	mux.HandleFunc("/webhooks/", webhookHandler)
	mux.HandleFunc("/api/v1/events/", externalHandler)

	// produceEventDirect builds a JSON payload and sends it through the standard
	// Avro-encoding pipeline (schema registry lookup → Avro encode → Kafka produce).
	produceEventDirect := func(ctx context.Context, topic string, payload map[string]any) error {
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal event payload: %w", err)
		}
		syntheticReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(string(payloadBytes)))
		syntheticReq.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		processAndProduceEvent(ctx, recorder, syntheticReq, topic)
		if recorder.Code >= 400 {
			return fmt.Errorf("event production failed for topic %s: status %d", topic, recorder.Code)
		}
		return nil
	}

	// Wire media handlers with shared Postgres + MinIO dependencies
	if handlers := initMediaHandlers(produceEventDirect); handlers != nil {
		mux.Handle("/api/v1/media/upload-url", handlers.Upload)
		mux.Handle("/api/v1/media/download-url", handlers.Download)
		mux.Handle("/api/v1/media/delete", handlers.Delete)
	} else {
		log.Println("WARNING: Media handlers not initialized — /api/v1/media/* will be unavailable")
	}

	// Wire async media intent endpoints (Upload Saga)
	intentHandler := &IntentHandler{ProduceEvent: produceEventDirect}
	mux.Handle("/api/v1/media/upload-intent", intentHandler)
	mux.Handle("/api/v1/media/download-intent", intentHandler)
	mux.Handle("/api/v1/media/delete-intent", intentHandler)

	server := &http.Server{Addr: ":8080", Handler: corsMiddleware(mux)}

	go func() {
		log.Println("Listening for internal webhook traffic on :8080/webhooks/{topic}...")
		log.Println("Listening for authenticated external traffic on :8080/api/v1/events/{topic}...")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %s\n", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}
