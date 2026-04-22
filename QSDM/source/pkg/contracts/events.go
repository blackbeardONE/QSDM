package contracts

import (
	"sync"
	"time"
)

// ContractEvent represents an on-chain event emitted during contract execution.
type ContractEvent struct {
	ContractID string                 `json:"contract_id"`
	EventName  string                 `json:"event_name"`
	Data       map[string]interface{} `json:"data"`
	BlockGas   int64                  `json:"block_gas"`
	Timestamp  time.Time              `json:"timestamp"`
	TxIndex    uint64                 `json:"tx_index"`
}

// EventSubscriber is called when a matching event is emitted.
type EventSubscriber func(event ContractEvent)

type eventSubscription struct {
	contractFilter string // empty = all contracts
	eventFilter    string // empty = all events
	handler        EventSubscriber
}

// EventIndex stores and indexes contract events for querying.
type EventIndex struct {
	events        []ContractEvent
	subscribers   []eventSubscription
	mu            sync.RWMutex
	maxEvents     int
	nextTxIndex   uint64
}

// NewEventIndex creates an event index with a configurable retention limit.
func NewEventIndex(maxEvents int) *EventIndex {
	if maxEvents <= 0 {
		maxEvents = 10000
	}
	return &EventIndex{
		events:    make([]ContractEvent, 0, 256),
		maxEvents: maxEvents,
	}
}

// Emit records an event and notifies all matching subscribers.
func (ei *EventIndex) Emit(contractID, eventName string, data map[string]interface{}, gasUsed int64) {
	ei.mu.Lock()
	evt := ContractEvent{
		ContractID: contractID,
		EventName:  eventName,
		Data:       data,
		BlockGas:   gasUsed,
		Timestamp:  time.Now(),
		TxIndex:    ei.nextTxIndex,
	}
	ei.nextTxIndex++
	ei.events = append(ei.events, evt)

	// Trim to retention limit
	if len(ei.events) > ei.maxEvents {
		ei.events = ei.events[len(ei.events)-ei.maxEvents:]
	}

	subs := make([]eventSubscription, len(ei.subscribers))
	copy(subs, ei.subscribers)
	ei.mu.Unlock()

	for _, sub := range subs {
		if sub.contractFilter != "" && sub.contractFilter != contractID {
			continue
		}
		if sub.eventFilter != "" && sub.eventFilter != eventName {
			continue
		}
		sub.handler(evt)
	}
}

// Subscribe registers a subscriber. Pass empty strings for contract/event to match all.
func (ei *EventIndex) Subscribe(contractFilter, eventFilter string, handler EventSubscriber) {
	ei.mu.Lock()
	defer ei.mu.Unlock()
	ei.subscribers = append(ei.subscribers, eventSubscription{
		contractFilter: contractFilter,
		eventFilter:    eventFilter,
		handler:        handler,
	})
}

// Query returns events matching the given filters. Pass empty strings to match all.
// Returns at most `limit` events, most recent first. offset skips the first N matches.
func (ei *EventIndex) Query(contractID, eventName string, limit, offset int) []ContractEvent {
	ei.mu.RLock()
	defer ei.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	var matches []ContractEvent
	skipped := 0
	for i := len(ei.events) - 1; i >= 0 && len(matches) < limit; i-- {
		e := ei.events[i]
		if contractID != "" && e.ContractID != contractID {
			continue
		}
		if eventName != "" && e.EventName != eventName {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		matches = append(matches, e)
	}
	return matches
}

// Count returns the total number of stored events.
func (ei *EventIndex) Count() int {
	ei.mu.RLock()
	defer ei.mu.RUnlock()
	return len(ei.events)
}

// CountByContract returns how many events exist for a given contract.
func (ei *EventIndex) CountByContract(contractID string) int {
	ei.mu.RLock()
	defer ei.mu.RUnlock()
	n := 0
	for _, e := range ei.events {
		if e.ContractID == contractID {
			n++
		}
	}
	return n
}
