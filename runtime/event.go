package runtime

import (
	"strings"
	"sync"
)

type EventType string

const (
	EventMessageCreated      EventType = "message.created"
	EventPartCreated         EventType = "message.part.created"
	EventPartUpdated         EventType = "message.part.updated"
	EventPartDelta           EventType = "message.part.delta"
	EventFileChanged         EventType = "file.changed"
	EventPermissionRequested EventType = "permission.requested"
	EventSessionCreated      EventType = "session.created"
	EventSessionUpdated      EventType = "session.updated"
)

type Event struct {
	Type      EventType
	SessionID string
	Data      any
}

type subscription struct {
	pattern string
	handler func(Event)
}

type EventBus struct {
	mu   sync.RWMutex
	subs []*subscription
}

func NewEventBus() *EventBus {
	return &EventBus{}
}

func (b *EventBus) Subscribe(pattern string, handler func(Event)) func() {
	sub := &subscription{pattern: pattern, handler: handler}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.subs {
			if s == sub {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				return
			}
		}
	}
}

func (b *EventBus) Publish(e Event) {
	b.mu.RLock()
	subs := make([]*subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	for _, s := range subs {
		if matchPattern(s.pattern, string(e.Type)) {
			s.handler(e)
		}
	}
}

func matchPattern(pattern, eventType string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return eventType == prefix || strings.HasPrefix(eventType, prefix+".")
	}
	return pattern == eventType
}
