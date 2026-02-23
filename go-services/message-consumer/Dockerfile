FROM golang:1.24-alpine AS builder

WORKDIR /app

# Enable faster caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -o message-consumer .

# Create lightweight production image
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/message-consumer .

CMD ["./message-consumer"]
