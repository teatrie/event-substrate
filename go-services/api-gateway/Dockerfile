# Build Stage
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o api-gateway

# Run Stage
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/api-gateway .

CMD ["./api-gateway"]
