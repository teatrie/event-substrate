package main

import (
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// topicAttr returns an OTel attribute for the Kafka topic name.
func topicAttr(topic string) attribute.KeyValue {
	return attribute.String("topic", topic)
}

// statusAttr returns an OTel attribute for the produce result status.
// Expected values: "success" or "error".
func statusAttr(status string) attribute.KeyValue {
	return attribute.String("status", status)
}

// Ensure the otelmetric package import is used (it is referenced via type in main.go).
var _ otelmetric.Int64Counter
