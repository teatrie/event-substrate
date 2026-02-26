package main

import (
	"context"
	"log"
)

// MessageHandler processes a deserialized Kafka event payload.
type MessageHandler interface {
	Handle(ctx context.Context, payload map[string]any) error
}

// TopicRouter dispatches Kafka messages to the correct handler based on topic.
type TopicRouter struct {
	handlers map[string]MessageHandler
}

// NewTopicRouter creates a router with the given topic→handler mappings.
func NewTopicRouter(handlers map[string]MessageHandler) *TopicRouter {
	return &TopicRouter{handlers: handlers}
}

// Route dispatches a message to the handler registered for the given topic.
// Returns true if a handler was found, false if the topic is unregistered.
func (r *TopicRouter) Route(ctx context.Context, topic string, payload map[string]any) bool {
	handler, ok := r.handlers[topic]
	if !ok {
		log.Printf("No handler for topic: %s (skipping)", topic)
		return false
	}
	if err := handler.Handle(ctx, payload); err != nil {
		log.Printf("Handler error for topic %s: %v", topic, err)
	}
	return true
}
