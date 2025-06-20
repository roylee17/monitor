package monitor

import (
	"context"
	"time"
)

// EventMonitor defines the interface for monitoring blockchain events
type EventMonitor interface {
	Start(ctx context.Context, processor *EventProcessor) error
	Stop() error
}

// EventHandler defines the interface for handling specific blockchain events
type EventHandler interface {
	HandleEvent(ctx context.Context, event Event) error
	CanHandle(event Event) bool
}

// Event represents a blockchain event with its data
type Event struct {
	Type       string
	Attributes map[string]string
	Height     int64
	TxHash     string
	Timestamp  time.Time
}

// EventProcessor processes incoming events using registered handlers
type EventProcessor struct {
	handlers []EventHandler
}

// NewEventProcessor creates a new event processor
func NewEventProcessor(handlers []EventHandler) *EventProcessor {
	return &EventProcessor{
		handlers: handlers,
	}
}

// Process handles an event by delegating to appropriate handlers
func (p *EventProcessor) Process(ctx context.Context, event Event) error {
	for _, handler := range p.handlers {
		if handler.CanHandle(event) {
			if err := handler.HandleEvent(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}
