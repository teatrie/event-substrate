FROM golang:1.25-alpine AS builder

WORKDIR /app

# Enable faster caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY *.go ./

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -o media-service .

# Create lightweight production image
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/media-service .

CMD ["./media-service"]
