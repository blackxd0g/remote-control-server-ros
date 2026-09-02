package connections

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const StaleAfter = 10 * time.Minute

type Record struct {
	Key              string         `json:"key"`
	Status           string         `json:"status"`
	ActorUserID      string         `json:"actor_user_id,omitempty"`
	ActorSessionID   string         `json:"actor_session_id,omitempty"`
	ControllerDevice string         `json:"controller_device_id,omitempty"`
	ControllerName   string         `json:"controller_name,omitempty"`
	ControllerLogin  string         `json:"controller_login,omitempty"`
	TargetID         string         `json:"target_rustdesk_id"`
	ConnectionType   int            `json:"connection_type"`
	IP               string         `json:"ip,omitempty"`
	StartedAt        time.Time      `json:"started_at"`
	LastSeenAt       time.Time      `json:"last_seen_at"`
	ClosedAt         *time.Time     `json:"closed_at,omitempty"`
	DurationSeconds  int64          `json:"duration_seconds"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type Snapshot struct {
	Active int      `json:"active"`
	Stale  int      `json:"stale"`
	Closed int      `json:"closed"`
	Items  []Record `json:"items"`
}

func Build(events []domain.AuditEvent, now time.Time) Snapshot {
	sort.SliceStable(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	items := map[string]*Record{}
	for _, event := range events {
		if event.Type != "connection_started" && event.Type != "connection_updated" && event.Type != "connection_closed" {
			continue
		}
		key := eventKey(event)
		value := items[key]
		if value == nil {
			value = &Record{Key: key, Status: "active", TargetID: event.TargetRustDeskID, LastSeenAt: event.OccurredAt, StartedAt: event.OccurredAt, Metadata: event.Metadata}
			items[key] = value
		}
		if event.Type == "connection_started" {
			value.StartedAt = event.OccurredAt
			value.ClosedAt, value.Status = nil, "active"
		}
		if event.OccurredAt.After(value.LastSeenAt) {
			value.LastSeenAt = event.OccurredAt
		}
		if value.ActorUserID == "" {
			value.ActorUserID, value.ActorSessionID = event.ActorUserID, event.ActorSessionID
		}
		if value.ControllerDevice == "" {
			value.ControllerDevice = event.ControllerDevice
		}
		if value.IP == "" {
			value.IP = event.IP
		}
		if text := stringValue(event.Metadata, "controller_display_name", "controller_name"); text != "" {
			value.ControllerName = text
		}
		if text := stringValue(event.Metadata, "controller_login"); text != "" {
			value.ControllerLogin = text
		}
		if connectionType := intValue(event.Metadata, "connection_type"); connectionType != 0 || value.ConnectionType == 0 {
			value.ConnectionType = connectionType
		}
		if event.Type == "connection_closed" && (value.ClosedAt == nil || event.OccurredAt.After(*value.ClosedAt)) {
			closed := event.OccurredAt
			value.ClosedAt, value.Status = &closed, "closed"
		}
	}
	values := make([]Record, 0, len(items))
	for _, value := range items {
		values = append(values, *value)
	}
	return finalize(values, now)
}

func BuildRecords(records []domain.ConnectionRecord, now time.Time) Snapshot {
	values := make([]Record, 0, len(records))
	for _, value := range records {
		values = append(values, Record{Key: value.Key, ActorUserID: value.ActorUserID, ActorSessionID: value.ActorSessionID,
			ControllerDevice: value.ControllerDevice, ControllerName: value.ControllerName, ControllerLogin: value.ControllerLogin,
			TargetID: value.TargetRustDeskID, ConnectionType: value.ConnectionType, IP: value.IP, StartedAt: value.StartedAt,
			LastSeenAt: value.LastSeenAt, ClosedAt: value.ClosedAt})
	}
	return finalize(values, now)
}

func finalize(values []Record, now time.Time) Snapshot {
	result := Snapshot{Items: make([]Record, 0, len(values))}
	for index := range values {
		value := &values[index]
		end := now
		if value.ClosedAt != nil {
			end = *value.ClosedAt
			result.Closed++
		} else if now.Sub(value.LastSeenAt) > StaleAfter {
			value.Status = "stale"
			result.Stale++
		} else {
			result.Active++
		}
		value.DurationSeconds = max(0, int64(end.Sub(value.StartedAt).Seconds()))
		result.Items = append(result.Items, *value)
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].LastSeenAt.After(result.Items[j].LastSeenAt) })
	return result
}

func eventKey(event domain.AuditEvent) string {
	connectionID := stringValue(event.Metadata, "connection_id")
	sessionID := stringValue(event.Metadata, "session_id")
	return fmt.Sprintf("%s:%s:%s:%s", event.TargetRustDeskID, event.ControllerDevice, connectionID, sessionID)
}

func stringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func intValue(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	}
	return 0
}
