package presence

import (
	"sort"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const (
	OnlineTTL = 3 * time.Minute
	IdleTTL   = 15 * time.Minute
)

type UserPresence struct {
	UserID         string    `json:"user_id"`
	Username       string    `json:"username"`
	DisplayName    string    `json:"display_name"`
	State          string    `json:"state"`
	LastSeenAt     time.Time `json:"last_seen_at,omitempty"`
	ClientDeviceID string    `json:"client_device_id,omitempty"`
	ActiveDevices  int       `json:"active_devices"`
}

type Snapshot struct {
	Online  int            `json:"online"`
	Idle    int            `json:"idle"`
	Offline int            `json:"offline"`
	Users   []UserPresence `json:"users"`
}

func Calculate(users []domain.User, sessions []domain.Session, now time.Time) Snapshot {
	byUser := make(map[string][]domain.Session)
	for _, session := range sessions {
		if session.RevokedAt == nil && session.ExpiresAt.After(now) {
			byUser[session.UserID] = append(byUser[session.UserID], session)
		}
	}
	result := Snapshot{Users: make([]UserPresence, 0, len(users))}
	for _, user := range users {
		value := UserPresence{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, State: "offline"}
		if !user.Enabled {
			value.State = "disabled"
		} else if user.ApprovalStatus != domain.ApprovalApproved {
			value.State = "pending"
		} else {
			devices := make(map[string]bool)
			for _, session := range byUser[user.ID] {
				if session.ClientDeviceID != "" {
					devices[session.ClientDeviceID] = true
				}
				if session.LastSeenAt.After(value.LastSeenAt) {
					value.LastSeenAt, value.ClientDeviceID = session.LastSeenAt, session.ClientDeviceID
				}
			}
			value.ActiveDevices = len(devices)
			age := now.Sub(value.LastSeenAt)
			if !value.LastSeenAt.IsZero() && age <= OnlineTTL {
				value.State = "online"
				result.Online++
			} else if !value.LastSeenAt.IsZero() && age <= IdleTTL {
				value.State = "idle"
				result.Idle++
			} else {
				result.Offline++
			}
		}
		result.Users = append(result.Users, value)
	}
	sort.SliceStable(result.Users, func(i, j int) bool {
		rank := map[string]int{"online": 0, "idle": 1, "offline": 2, "pending": 3, "disabled": 4}
		if rank[result.Users[i].State] != rank[result.Users[j].State] {
			return rank[result.Users[i].State] < rank[result.Users[j].State]
		}
		return result.Users[i].LastSeenAt.After(result.Users[j].LastSeenAt)
	})
	return result
}
