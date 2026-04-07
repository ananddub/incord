package event

import "sync"

// SubscriptionManager manages per-resource subscriptions.
// Clients subscribe to a resource (channel_id, guild_id, user_id) and receive events.
type SubscriptionManager[T any] struct {
	mu   sync.RWMutex
	subs map[string]map[string]chan T // resource_id -> sub_id -> channel
}

func NewSubscriptionManager[T any]() *SubscriptionManager[T] {
	return &SubscriptionManager[T]{
		subs: make(map[string]map[string]chan T),
	}
}

// Subscribe returns a channel that receives events for the given resource.
func (m *SubscriptionManager[T]) Subscribe(resourceID, subID string, bufSize int) chan T {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.subs[resourceID] == nil {
		m.subs[resourceID] = make(map[string]chan T)
	}

	ch := make(chan T, bufSize)
	m.subs[resourceID][subID] = ch
	return ch
}

// Unsubscribe removes a subscription.
func (m *SubscriptionManager[T]) Unsubscribe(resourceID, subID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if subs, ok := m.subs[resourceID]; ok {
		if ch, ok := subs[subID]; ok {
			close(ch)
			delete(subs, subID)
		}
		if len(subs) == 0 {
			delete(m.subs, resourceID)
		}
	}
}

// Publish sends an event to all subscribers of a resource. Non-blocking - drops if buffer full.
func (m *SubscriptionManager[T]) Publish(resourceID string, event T) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.subs[resourceID] {
		select {
		case ch <- event:
		default:
			// drop if buffer full
		}
	}
}

// Count returns the number of subscribers for a resource.
func (m *SubscriptionManager[T]) Count(resourceID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.subs[resourceID])
}
