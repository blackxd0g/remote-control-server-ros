package events

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	UserUpdated                = "USER_UPDATED"
	UserDisabled               = "USER_DISABLED"
	SessionCreated             = "SESSION_CREATED"
	SessionRevoked             = "SESSION_REVOKED"
	SessionRevokedAll          = "SESSION_REVOKED_ALL"
	ACLUpdated                 = "ACL_UPDATED"
	StrategyUpdated            = "STRATEGY_UPDATED"
	UserGroupMembershipUpdated = "USER_GROUP_MEMBERSHIP_UPDATED"
	RelayUpdated               = "RELAY_UPDATED"
	DeviceUpdated              = "DEVICE_UPDATED"
	AuditRecorded              = "AUDIT_RECORDED"
	ConfigurationUpdated       = "CONFIGURATION_UPDATED"
	AutomationTriggered        = "AUTOMATION_TRIGGERED"
)

type Event struct {
	SourceID   string          `json:"source_id"`
	Revision   int64           `json:"revision"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

type Hub struct {
	sourceID    string
	revision    atomic.Int64
	mutex       sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan Event
	history     []Event
}

const historyLimit = 1024

func NewHub() *Hub {
	return &Hub{sourceID: uuid.NewString(), subscribers: make(map[uint64]chan Event)}
}

func (h *Hub) Revision() int64  { return h.revision.Load() }
func (h *Hub) SourceID() string { return h.sourceID }

func (h *Hub) Publish(eventType string, payload any) Event {
	data, _ := json.Marshal(payload)
	event := Event{SourceID: h.sourceID, Revision: h.revision.Add(1), Type: eventType, OccurredAt: time.Now().UTC(), Payload: data}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.history = append(h.history, event)
	if len(h.history) > historyLimit {
		copy(h.history, h.history[len(h.history)-historyLimit:])
		h.history = h.history[:historyLimit]
	}
	for _, subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
	return event
}

func (h *Hub) Subscribe() (<-chan Event, func()) {
	channel, _, unsubscribe := h.SubscribeAfter("", 0)
	return channel, unsubscribe
}

// SubscribeAfter atomically installs a subscriber and returns events that were
// published after the caller's cursor. This closes the snapshot-to-stream race:
// an HBBS node can reconcile, then subscribe without losing events published in
// between. A changed source ID means the API restarted, so all retained events
// from the new process are replayed.
func (h *Hub) SubscribeAfter(sourceID string, revision int64) (<-chan Event, []Event, func()) {
	h.mutex.Lock()
	h.nextID++
	id := h.nextID
	channel := make(chan Event, 64)
	h.subscribers[id] = channel
	backlog := make([]Event, 0, len(h.history))
	for _, event := range h.history {
		if sourceID != h.sourceID || event.Revision > revision {
			backlog = append(backlog, event)
		}
	}
	h.mutex.Unlock()
	return channel, backlog, func() {
		h.mutex.Lock()
		if existing, ok := h.subscribers[id]; ok {
			delete(h.subscribers, id)
			close(existing)
		}
		h.mutex.Unlock()
	}
}
