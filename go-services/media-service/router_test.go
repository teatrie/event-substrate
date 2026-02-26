package main

import (
	"context"
	"fmt"
	"testing"
)

// mockHandler records calls for testing.
type mockHandler struct {
	calls   []map[string]any
	failErr error
}

func (m *mockHandler) Handle(ctx context.Context, payload map[string]any) error {
	m.calls = append(m.calls, payload)
	return m.failErr
}

func TestTopicRouter_RoutesToCorrectHandler(t *testing.T) {
	uploadHandler := &mockHandler{}
	downloadHandler := &mockHandler{}
	deleteHandler := &mockHandler{}
	completedHandler := &mockHandler{}
	expiredHandler := &mockHandler{}

	router := NewTopicRouter(map[string]MessageHandler{
		"public.media.upload.approved":   uploadHandler,
		"public.media.upload.events":     completedHandler,
		"public.media.expired.events":    expiredHandler,
		"internal.media.download.intent": downloadHandler,
		"internal.media.delete.intent":   deleteHandler,
	})

	ctx := context.Background()

	tests := []struct {
		topic   string
		handler *mockHandler
	}{
		{"public.media.upload.approved", uploadHandler},
		{"public.media.upload.events", completedHandler},
		{"public.media.expired.events", expiredHandler},
		{"internal.media.download.intent", downloadHandler},
		{"internal.media.delete.intent", deleteHandler},
	}

	for _, tt := range tests {
		payload := map[string]any{"topic": tt.topic}
		found := router.Route(ctx, tt.topic, payload)
		if !found {
			t.Errorf("Route(%q) returned false, expected true", tt.topic)
		}
		if len(tt.handler.calls) == 0 {
			t.Errorf("Handler for %q was not called", tt.topic)
		}
	}
}

func TestTopicRouter_UnknownTopicSkipped(t *testing.T) {
	router := NewTopicRouter(map[string]MessageHandler{})
	found := router.Route(context.Background(), "unknown.topic", map[string]any{})
	if found {
		t.Error("Route() returned true for unknown topic, expected false")
	}
}

func TestTopicRouter_HandlerErrorDoesNotPanic(t *testing.T) {
	handler := &mockHandler{failErr: fmt.Errorf("db connection failed")}
	router := NewTopicRouter(map[string]MessageHandler{
		"test.topic": handler,
	})

	// Should not panic
	found := router.Route(context.Background(), "test.topic", map[string]any{"key": "value"})
	if !found {
		t.Error("Route() returned false, expected true (handler exists)")
	}
	if len(handler.calls) != 1 {
		t.Errorf("Handler called %d times, want 1", len(handler.calls))
	}
}
