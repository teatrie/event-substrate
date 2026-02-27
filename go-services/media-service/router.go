package main

import (
	"context"
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
		log.Warn().Str("topic", topic).Msg("No handler for topic (skipping)")
		return false
	}
	if err := handler.Handle(ctx, payload); err != nil {
		log.Error().Err(err).Str("topic", topic).Msg("Handler error")
	}
	return true
}
