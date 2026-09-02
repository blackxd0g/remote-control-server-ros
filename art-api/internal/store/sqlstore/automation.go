package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

func (s *Store) CreateAutomationRule(ctx context.Context, value domain.AutomationRule) error {
	eventsJSON, _ := json.Marshal(value.EventTypes)
	conditionsJSON, _ := json.Marshal(value.Conditions)
	actionsJSON, _ := json.Marshal(value.Actions)
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO automation_rules(id,name,event_types,conditions,actions,severity,throttle_seconds,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`), value.ID, value.Name, string(eventsJSON), string(conditionsJSON), string(actionsJSON), value.Severity, value.ThrottleSeconds, value.Enabled, millis(value.CreatedAt), millis(value.UpdatedAt))
	return err
}

func (s *Store) ListAutomationRules(ctx context.Context) ([]domain.AutomationRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,event_types,conditions,actions,severity,throttle_seconds,enabled,created_at,updated_at FROM automation_rules ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.AutomationRule{}
	for rows.Next() {
		var value domain.AutomationRule
		var eventTypes, conditions, actions string
		var createdAt, updatedAt int64
		if err = rows.Scan(&value.ID, &value.Name, &eventTypes, &conditions, &actions, &value.Severity, &value.ThrottleSeconds, &value.Enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(eventTypes), &value.EventTypes)
		_ = json.Unmarshal([]byte(conditions), &value.Conditions)
		_ = json.Unmarshal([]byte(actions), &value.Actions)
		value.CreatedAt, value.UpdatedAt = fromMillis(createdAt), fromMillis(updatedAt)
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) UpdateAutomationRule(ctx context.Context, value domain.AutomationRule) error {
	eventsJSON, _ := json.Marshal(value.EventTypes)
	conditionsJSON, _ := json.Marshal(value.Conditions)
	actionsJSON, _ := json.Marshal(value.Actions)
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE automation_rules SET name=?,event_types=?,conditions=?,actions=?,severity=?,throttle_seconds=?,enabled=?,updated_at=? WHERE id=?`), value.Name, string(eventsJSON), string(conditionsJSON), string(actionsJSON), value.Severity, value.ThrottleSeconds, value.Enabled, millis(value.UpdatedAt), value.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (s *Store) DeleteAutomationRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM automation_rules WHERE id=?`), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (s *Store) CreateAutomationRun(ctx context.Context, value domain.AutomationRun) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO automation_runs(id,rule_id,event_type,event_id,status,message,created_at) VALUES(?,?,?,?,?,?,?)`), value.ID, value.RuleID, value.EventType, value.EventID, value.Status, value.Message, millis(value.CreatedAt))
	return err
}

func (s *Store) ListAutomationRuns(ctx context.Context, ruleID string, limit int) ([]domain.AutomationRun, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query, args := `SELECT id,rule_id,event_type,event_id,status,message,created_at FROM automation_runs`, []any{}
	if ruleID != "" {
		query += ` WHERE rule_id=?`
		args = append(args, ruleID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.AutomationRun{}
	for rows.Next() {
		var value domain.AutomationRun
		var createdAt int64
		if err = rows.Scan(&value.ID, &value.RuleID, &value.EventType, &value.EventID, &value.Status, &value.Message, &createdAt); err != nil {
			return nil, err
		}
		value.CreatedAt = fromMillis(createdAt)
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) LastAutomationRun(ctx context.Context, ruleID string) (time.Time, error) {
	var createdAt int64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT created_at FROM automation_runs WHERE rule_id=? AND status='completed' ORDER BY created_at DESC LIMIT 1`), ruleID).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return fromMillis(createdAt), err
}
