package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	dialect string
}

func (s *Store) ListRuntimeSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM runtime_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, rows.Err()
}

func (s *Store) UpsertRuntimeSettings(ctx context.Context, values map[string]string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := s.bind(`INSERT INTO runtime_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`)
	for key, value := range values {
		if _, err = tx.ExecContext(ctx, query, key, value, millis(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func Open(driver, dsn string) (*Store, error) {
	databaseDriver := "sqlite"
	if driver == "postgres" {
		databaseDriver = "pgx"
	} else {
		if err := os.MkdirAll(filepath.Dir(dsn), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		dsn = "file:" + filepath.ToSlash(dsn) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	}
	db, err := sql.Open(databaseDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if driver == "sqlite" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
	}
	return &Store{db: db, dialect: driver}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Backup(ctx context.Context, destination string) error {
	if s.dialect != "sqlite" {
		return domain.ErrUnsupported
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup destination must not exist")
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, destination)
	return err
}

func (s *Store) InspectBackup(ctx context.Context, path string) (domain.BackupInspection, error) {
	if s.dialect != "sqlite" {
		return domain.BackupInspection{}, domain.ErrUnsupported
	}
	info, err := os.Stat(path)
	if err != nil {
		return domain.BackupInspection{}, err
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=query_only(ON)&_pragma=foreign_keys(ON)")
	if err != nil {
		return domain.BackupInspection{}, err
	}
	defer database.Close()
	result := domain.BackupInspection{SizeBytes: info.Size(), ModifiedAt: info.ModTime().UTC()}
	if err = database.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result.QuickCheck); err != nil {
		return domain.BackupInspection{}, fmt.Errorf("SQLite quick check: %w", err)
	}
	if result.QuickCheck != "ok" {
		return result, fmt.Errorf("SQLite quick check failed: %s", result.QuickCheck)
	}
	if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&result.SchemaTables); err != nil {
		return domain.BackupInspection{}, err
	}
	for table, target := range map[string]*int64{"users": &result.Users, "devices": &result.Devices, "sessions": &result.Sessions} {
		var exists int
		if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			return domain.BackupInspection{}, err
		}
		if exists != 1 {
			return result, fmt.Errorf("required table %q is missing", table)
		}
		if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(target); err != nil {
			return domain.BackupInspection{}, err
		}
	}
	result.Valid = true
	return result, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
            id TEXT PRIMARY KEY,
            username TEXT NOT NULL UNIQUE,
            email TEXT NOT NULL DEFAULT '',
            phone TEXT NOT NULL DEFAULT '',
            password_hash TEXT NOT NULL,
            display_name TEXT NOT NULL DEFAULT '',
            role TEXT NOT NULL,
            enabled BOOLEAN NOT NULL DEFAULT TRUE,
            approval_status TEXT NOT NULL DEFAULT 'approved',
            token_version BIGINT NOT NULL DEFAULT 1,
            created_at BIGINT NOT NULL,
            updated_at BIGINT NOT NULL,
            last_login_at BIGINT,
            force_relogin_at BIGINT
        )`,
		`CREATE TABLE IF NOT EXISTS sessions (
            id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL REFERENCES users(id),
            created_at BIGINT NOT NULL,
            expires_at BIGINT NOT NULL,
            revoked_at BIGINT,
            last_seen_at BIGINT NOT NULL,
            ip TEXT NOT NULL DEFAULT '',
            user_agent TEXT NOT NULL DEFAULT '',
            client_device_id TEXT NOT NULL DEFAULT ''
        )`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_active ON sessions(user_id, expires_at, revoked_at)`,
		`CREATE TABLE IF NOT EXISTS auth_challenges (
            id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL REFERENCES users(id),
            token_version BIGINT NOT NULL,
            created_at BIGINT NOT NULL,
            expires_at BIGINT NOT NULL,
            ip TEXT NOT NULL DEFAULT '',
            user_agent TEXT NOT NULL DEFAULT '',
            client_device_id TEXT NOT NULL DEFAULT '',
            rustdesk_id TEXT NOT NULL DEFAULT '',
            client_uuid TEXT NOT NULL DEFAULT '',
            platform TEXT NOT NULL DEFAULT '',
            client_type TEXT NOT NULL DEFAULT '',
            device_name TEXT NOT NULL DEFAULT ''
        )`,
		`CREATE INDEX IF NOT EXISTS idx_auth_challenges_expiry ON auth_challenges(expires_at)`,
		`CREATE TABLE IF NOT EXISTS oidc_auth_requests (
            state TEXT PRIMARY KEY, poll_code TEXT NOT NULL UNIQUE, provider TEXT NOT NULL,
			verifier TEXT NOT NULL, nonce TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT '', link_user_id TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
            rustdesk_id TEXT NOT NULL DEFAULT '', client_uuid TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL DEFAULT '',
            client_type TEXT NOT NULL DEFAULT '', device_name TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '',
            created_at BIGINT NOT NULL, expires_at BIGINT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_oidc_auth_expiry ON oidc_auth_requests(expires_at)`,
		`CREATE TABLE IF NOT EXISTS oidc_identities (
            provider TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL REFERENCES users(id),
            email TEXT NOT NULL DEFAULT '', created_at BIGINT NOT NULL, PRIMARY KEY(provider,subject)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_oidc_identity_user ON oidc_identities(user_id)`,
		`CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
            user_id TEXT NOT NULL REFERENCES users(id),
            code_hash TEXT NOT NULL,
            created_at BIGINT NOT NULL,
            PRIMARY KEY(user_id, code_hash)
        )`,
		`CREATE TABLE IF NOT EXISTS audit_events (
            id TEXT PRIMARY KEY,
            occurred_at BIGINT NOT NULL,
            type TEXT NOT NULL,
            actor_user_id TEXT NOT NULL DEFAULT '',
            actor_session_id TEXT NOT NULL DEFAULT '',
            controller_device_id TEXT NOT NULL DEFAULT '',
            target_rustdesk_id TEXT NOT NULL DEFAULT '',
            ip TEXT NOT NULL DEFAULT '',
            result TEXT NOT NULL DEFAULT '',
            reason TEXT NOT NULL DEFAULT '',
            metadata TEXT NOT NULL DEFAULT '{}'
        )`,
		`CREATE INDEX IF NOT EXISTS idx_audit_occurred_at ON audit_events(occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_type_occurred ON audit_events(type, occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_result_occurred ON audit_events(result, occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_actor_occurred ON audit_events(actor_user_id, occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_target_occurred ON audit_events(target_rustdesk_id, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS connection_records (
            connection_key TEXT PRIMARY KEY,
            actor_user_id TEXT NOT NULL DEFAULT '', actor_session_id TEXT NOT NULL DEFAULT '',
            controller_device_id TEXT NOT NULL DEFAULT '', controller_name TEXT NOT NULL DEFAULT '', controller_login TEXT NOT NULL DEFAULT '',
            target_rustdesk_id TEXT NOT NULL, connection_type INTEGER NOT NULL DEFAULT 0, ip TEXT NOT NULL DEFAULT '',
            started_at BIGINT NOT NULL, last_seen_at BIGINT NOT NULL, closed_at BIGINT
        )`,
		`CREATE INDEX IF NOT EXISTS idx_connections_last_seen ON connection_records(last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_actor_started ON connection_records(actor_user_id,started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_target_started ON connection_records(target_rustdesk_id,started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_connections_open ON connection_records(closed_at,last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_created ON sessions(user_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_revoked_at ON sessions(revoked_at)`,
		`CREATE TABLE IF NOT EXISTS login_attempts (id TEXT PRIMARY KEY, username TEXT NOT NULL, occurred_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_login_attempts_user_time ON login_attempts(username,occurred_at)`,
		`CREATE TABLE IF NOT EXISTS account_lockouts (username TEXT PRIMARY KEY, locked_until BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS devices (
            rustdesk_id TEXT PRIMARY KEY,
            client_uuid TEXT NOT NULL DEFAULT '',
            hostname TEXT NOT NULL DEFAULT '',
            alias TEXT NOT NULL DEFAULT '',
            platform TEXT NOT NULL DEFAULT '',
            version TEXT NOT NULL DEFAULT '',
            cpu TEXT NOT NULL DEFAULT '',
            memory TEXT NOT NULL DEFAULT '',
            os_username TEXT NOT NULL DEFAULT '',
            last_seen_ip TEXT NOT NULL DEFAULT '',
            online BOOLEAN NOT NULL DEFAULT FALSE,
            last_seen BIGINT NOT NULL,
            owner_user_id TEXT NOT NULL DEFAULT '',
            group_id TEXT NOT NULL DEFAULT '',
            tags TEXT NOT NULL DEFAULT '[]',
            created_at BIGINT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_devices_owner_user_id ON devices(owner_user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_group_id ON devices(group_id)`,
		`CREATE TABLE IF NOT EXISTS groups (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            description TEXT NOT NULL DEFAULT '',
            kind TEXT NOT NULL,
            created_at BIGINT NOT NULL,
            updated_at BIGINT NOT NULL,
            UNIQUE(kind, name)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_groups_kind_name ON groups(kind, name)`,
		`CREATE TABLE IF NOT EXISTS user_group_memberships (group_id TEXT NOT NULL, user_id TEXT NOT NULL, created_at BIGINT NOT NULL, PRIMARY KEY(group_id,user_id))`,
		`CREATE INDEX IF NOT EXISTS idx_user_group_memberships_user ON user_group_memberships(user_id)`,
		`CREATE TABLE IF NOT EXISTS address_books (id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, owner_user_id TEXT NOT NULL DEFAULT '', created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS address_book_grants (id TEXT PRIMARY KEY, address_book_id TEXT NOT NULL, subject_type TEXT NOT NULL, subject_id TEXT NOT NULL, permission TEXT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE(address_book_id,subject_type,subject_id))`,
		`CREATE INDEX IF NOT EXISTS idx_address_book_grants_book ON address_book_grants(address_book_id)`,
		`CREATE INDEX IF NOT EXISTS idx_address_book_grants_subject ON address_book_grants(subject_type,subject_id)`,
		`CREATE TABLE IF NOT EXISTS address_book_entries (id TEXT PRIMARY KEY, address_book_id TEXT NOT NULL, rustdesk_id TEXT NOT NULL, alias TEXT NOT NULL DEFAULT '', folder TEXT NOT NULL DEFAULT '', favourite BOOLEAN NOT NULL DEFAULT FALSE, tags TEXT NOT NULL DEFAULT '[]', created_at BIGINT NOT NULL, UNIQUE(address_book_id,rustdesk_id))`,
		`CREATE INDEX IF NOT EXISTS idx_address_book_entries_book ON address_book_entries(address_book_id)`,
		`CREATE TABLE IF NOT EXISTS address_book_tags (id TEXT PRIMARY KEY, address_book_id TEXT NOT NULL, name TEXT NOT NULL, color BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE(address_book_id,name))`,
		`CREATE TABLE IF NOT EXISTS api_tokens (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, prefix TEXT NOT NULL, scopes TEXT NOT NULL DEFAULT '[]', created_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, last_used_at BIGINT, revoked_at BIGINT)`,
		`CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_address_book_tags_book ON address_book_tags(address_book_id)`,
		`CREATE TABLE IF NOT EXISTS relay_servers (id TEXT PRIMARY KEY, name TEXT NOT NULL, hostname TEXT NOT NULL, port INTEGER NOT NULL, region TEXT NOT NULL DEFAULT '', enabled BOOLEAN NOT NULL DEFAULT TRUE, health TEXT NOT NULL DEFAULT 'unknown', latency_ms INTEGER NOT NULL DEFAULT 0, connections INTEGER NOT NULL DEFAULT 0, bandwidth BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, UNIQUE(hostname,port))`,
		`CREATE TABLE IF NOT EXISTS relay_metrics (relay_id TEXT NOT NULL, recorded_at BIGINT NOT NULL, health TEXT NOT NULL, latency_ms INTEGER NOT NULL DEFAULT 0, connections INTEGER NOT NULL DEFAULT 0, bandwidth BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(relay_id,recorded_at))`,
		`CREATE INDEX IF NOT EXISTS idx_relay_metrics_time ON relay_metrics(recorded_at)`,
		`CREATE TABLE IF NOT EXISTS webhooks (id TEXT PRIMARY KEY, name TEXT NOT NULL, url TEXT NOT NULL, events TEXT NOT NULL DEFAULT '[]', enabled BOOLEAN NOT NULL DEFAULT TRUE, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (id TEXT PRIMARY KEY, webhook_id TEXT NOT NULL, event_type TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, response_code INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', next_attempt BIGINT NOT NULL, created_at BIGINT NOT NULL, delivered_at BIGINT)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due ON webhook_deliveries(status,next_attempt)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_hook ON webhook_deliveries(webhook_id,created_at)`,
		`CREATE TABLE IF NOT EXISTS notifications (id TEXT PRIMARY KEY, type TEXT NOT NULL, severity TEXT NOT NULL, title TEXT NOT NULL, message TEXT NOT NULL, resource TEXT NOT NULL DEFAULT '', created_at BIGINT NOT NULL, read_at BIGINT)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications(read_at,created_at)`,
		`CREATE TABLE IF NOT EXISTS automation_rules (id TEXT PRIMARY KEY, name TEXT NOT NULL, event_types TEXT NOT NULL DEFAULT '[]', conditions TEXT NOT NULL DEFAULT '{}', actions TEXT NOT NULL DEFAULT '[]', severity TEXT NOT NULL DEFAULT 'info', throttle_seconds INTEGER NOT NULL DEFAULT 0, enabled BOOLEAN NOT NULL DEFAULT TRUE, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_rules_enabled ON automation_rules(enabled,updated_at)`,
		`CREATE TABLE IF NOT EXISTS automation_runs (id TEXT PRIMARY KEY, rule_id TEXT NOT NULL, event_type TEXT NOT NULL, event_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', created_at BIGINT NOT NULL, FOREIGN KEY(rule_id) REFERENCES automation_rules(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_runs_rule_created ON automation_runs(rule_id,created_at)`,
		`CREATE TABLE IF NOT EXISTS cluster_nodes (id TEXT PRIMARY KEY, service TEXT NOT NULL, version TEXT NOT NULL DEFAULT '', address TEXT NOT NULL DEFAULT '', started_at BIGINT NOT NULL, last_seen_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_nodes_seen ON cluster_nodes(service,last_seen_at)`,
		`CREATE TABLE IF NOT EXISTS cluster_leases (name TEXT PRIMARY KEY, owner_id TEXT NOT NULL, expires_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_cluster_leases_expiry ON cluster_leases(expires_at)`,
		`CREATE TABLE IF NOT EXISTS client_profiles (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', platform TEXT NOT NULL DEFAULT 'all', settings TEXT NOT NULL DEFAULT '{}', branding TEXT NOT NULL DEFAULT '{}', version BIGINT NOT NULL DEFAULT 1, enabled BOOLEAN NOT NULL DEFAULT TRUE, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS client_profile_assignments (id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, scope_type TEXT NOT NULL, scope_id TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 100, created_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_client_profile_assignment_scope ON client_profile_assignments(scope_type,scope_id,priority)`,
		`CREATE TABLE IF NOT EXISTS client_builds (id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, target_os TEXT NOT NULL, architecture TEXT NOT NULL, format TEXT NOT NULL, status TEXT NOT NULL, artifact_name TEXT NOT NULL DEFAULT '', media_type TEXT NOT NULL DEFAULT '', sha256 TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', artifact TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL, worker_id TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, started_at BIGINT, lease_until BIGINT, completed_at BIGINT)`,
		`CREATE INDEX IF NOT EXISTS idx_client_builds_created ON client_builds(created_at)`,
		`CREATE TABLE IF NOT EXISTS builder_workers (id TEXT PRIMARY KEY, name TEXT NOT NULL, hostname TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '', formats TEXT NOT NULL DEFAULT '[]', platforms TEXT NOT NULL DEFAULT '[]', architectures TEXT NOT NULL DEFAULT '[]', concurrency INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'online', token_hash TEXT NOT NULL DEFAULT '', last_seen_at BIGINT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS runtime_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS user_preferences (user_id TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, updated_at BIGINT NOT NULL, PRIMARY KEY(user_id,key))`,
		`CREATE INDEX IF NOT EXISTS idx_builder_workers_seen ON builder_workers(last_seen_at)`,
		`CREATE TABLE IF NOT EXISTS acl_rules (id TEXT PRIMARY KEY, name TEXT NOT NULL, subject_type TEXT NOT NULL, subject_id TEXT NOT NULL DEFAULT '', target_type TEXT NOT NULL, target_id TEXT NOT NULL DEFAULT '', permissions TEXT NOT NULL DEFAULT '[]', effect TEXT NOT NULL DEFAULT 'allow', enabled BOOLEAN NOT NULL DEFAULT TRUE, priority INTEGER NOT NULL DEFAULT 100, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_acl_priority ON acl_rules(enabled, priority)`,
		`CREATE TABLE IF NOT EXISTS strategies (id TEXT PRIMARY KEY, name TEXT NOT NULL, scope_type TEXT NOT NULL, scope_id TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 100, settings TEXT NOT NULL DEFAULT '{}', enabled BOOLEAN NOT NULL DEFAULT TRUE, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_strategy_scope ON strategies(scope_type, scope_id, priority)`,
		`CREATE TABLE IF NOT EXISTS roles (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', permissions TEXT NOT NULL DEFAULT '[]', system BOOLEAN NOT NULL DEFAULT FALSE, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
	}
	for _, column := range []string{
		"cpu TEXT NOT NULL DEFAULT ''",
		"memory TEXT NOT NULL DEFAULT ''",
		"os_username TEXT NOT NULL DEFAULT ''",
		"last_seen_ip TEXT NOT NULL DEFAULT ''",
	} {
		if err := s.ensureDeviceColumn(ctx, strings.Fields(column)[0], column); err != nil {
			return err
		}
	}
	if err := s.ensureTableColumn(ctx, "strategies", "deleted", "deleted BOOLEAN NOT NULL DEFAULT FALSE"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "users", "totp_secret", "totp_secret TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "users", "totp_enabled", "totp_enabled BOOLEAN NOT NULL DEFAULT FALSE"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "users", "approval_status", "approval_status TEXT NOT NULL DEFAULT 'approved'"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "users", "phone", "phone TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "oidc_auth_requests", "link_user_id", "link_user_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "address_book_entries", "tags", "tags TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "client_builds", "artifact", "artifact TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "client_builds", "media_type", "media_type TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "acl_rules", "effect", "effect TEXT NOT NULL DEFAULT 'allow'"); err != nil {
		return err
	}
	if err := s.ensureTableColumn(ctx, "builder_workers", "token_hash", "token_hash TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_builder_workers_token ON builder_workers(token_hash) WHERE token_hash<>''`); err != nil {
		return fmt.Errorf("migrate builder worker credentials: %w", err)
	}
	for _, column := range []struct{ name, definition string }{
		{"worker_id", "worker_id TEXT NOT NULL DEFAULT ''"},
		{"attempts", "attempts INTEGER NOT NULL DEFAULT 0"},
		{"started_at", "started_at BIGINT"},
		{"lease_until", "lease_until BIGINT"},
	} {
		if err := s.ensureTableColumn(ctx, "client_builds", column.name, column.definition); err != nil {
			return err
		}
	}
	for name, definition := range map[string]string{
		"public_key": "public_key TEXT NOT NULL DEFAULT ''", "deployed": "deployed BOOLEAN NOT NULL DEFAULT FALSE",
		"deployed_by": "deployed_by TEXT NOT NULL DEFAULT ''", "deployed_at": "deployed_at BIGINT", "archived_at": "archived_at BIGINT",
	} {
		if err := s.ensureTableColumn(ctx, "devices", name, definition); err != nil {
			return err
		}
	}
	for name, definition := range map[string]string{
		"username": "username TEXT NOT NULL DEFAULT ''", "hostname": "hostname TEXT NOT NULL DEFAULT ''",
		"platform": "platform TEXT NOT NULL DEFAULT ''", "force_relay": "force_relay BOOLEAN NOT NULL DEFAULT FALSE",
		"rdp_port": "rdp_port TEXT NOT NULL DEFAULT ''", "rdp_username": "rdp_username TEXT NOT NULL DEFAULT ''",
		"login_name": "login_name TEXT NOT NULL DEFAULT ''", "same_server": "same_server BOOLEAN NOT NULL DEFAULT TRUE",
	} {
		if err := s.ensureTableColumn(ctx, "address_book_entries", name, definition); err != nil {
			return err
		}
	}
	for _, group := range []domain.Group{
		{ID: domain.PendingUsersGroupID, Name: "Новые пользователи", Description: "Ожидают одобрения администратора", Kind: domain.GroupKindUser},
		{ID: domain.ApprovedUsersGroupID, Name: "Авторизованные пользователи", Description: "Регистрация одобрена администратором", Kind: domain.GroupKindUser},
	} {
		now := millis(time.Now().UTC())
		if _, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO groups(id,name,description,kind,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`), group.ID, group.Name, group.Description, group.Kind, now, now); err != nil {
			return fmt.Errorf("create registration group: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO user_group_memberships(group_id,user_id,created_at) SELECT ?,id,? FROM users WHERE approval_status=? ON CONFLICT(group_id,user_id) DO NOTHING`), domain.ApprovedUsersGroupID, millis(time.Now().UTC()), domain.ApprovalApproved); err != nil {
		return fmt.Errorf("assign existing approved users: %w", err)
	}
	now := millis(time.Now().UTC())
	for _, role := range []domain.RoleDefinition{
		{ID: domain.RoleAdmin, Name: "Администратор", Description: "Полный доступ ко всем функциям", Permissions: []string{domain.PermissionAll}, System: true},
		{ID: domain.RoleUser, Name: "Пользователь", Description: "Доступ к клиенту и личному кабинету", Permissions: []string{}, System: true},
	} {
		permissions, _ := json.Marshal(role.Permissions)
		if _, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO roles(id,name,description,permissions,system,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`), role.ID, role.Name, role.Description, string(permissions), role.System, now, now); err != nil {
			return fmt.Errorf("create built-in role: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureDeviceColumn(ctx context.Context, name, definition string) error {
	return s.ensureTableColumn(ctx, "devices", name, definition)
}

func (s *Store) ensureTableColumn(ctx context.Context, table, name, definition string) error {
	var count int
	if s.dialect == "postgres" {
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, name).Scan(&count)
		if err != nil {
			return fmt.Errorf("inspect devices schema: %w", err)
		}
	} else {
		rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if err != nil {
			return fmt.Errorf("inspect devices schema: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notNull, primaryKey int
			var columnName, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				return err
			}
			if columnName == name {
				count = 1
			}
		}
	}
	if count != 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, name, err)
	}
	return nil
}

func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var count int64
	return count, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
}

func (s *Store) CreateUser(ctx context.Context, user domain.User) error {
	if user.ApprovalStatus == "" {
		user.ApprovalStatus = domain.ApprovalApproved
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO users
		(id, username, email, password_hash, display_name, role, enabled, approval_status, token_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), user.ID, strings.ToLower(user.Username), user.Email,
		user.PasswordHash, user.DisplayName, user.Role, user.Enabled, user.ApprovalStatus, user.TokenVersion,
		millis(user.CreatedAt), millis(user.UpdatedAt))
	if err != nil {
		return err
	}
	if user.ApprovalStatus == domain.ApprovalApproved {
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO user_group_memberships(group_id,user_id,created_at) VALUES(?,?,?)`), domain.ApprovedUsersGroupID, user.ID, millis(user.CreatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateRegisteredUser(ctx context.Context, user domain.User) error {
	user.ApprovalStatus = domain.ApprovalPending
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO users(id,username,email,password_hash,display_name,role,enabled,approval_status,token_version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`), user.ID, strings.ToLower(user.Username), user.Email, user.PasswordHash, user.DisplayName, user.Role, user.Enabled, user.ApprovalStatus, user.TokenVersion, millis(user.CreatedAt), millis(user.UpdatedAt))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO user_group_memberships(group_id,user_id,created_at) VALUES(?,?,?)`), domain.PendingUsersGroupID, user.ID, millis(user.CreatedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]domain.User, 0)
	for rows.Next() {
		user, scanErr := s.scanUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) FindUserByUsername(ctx context.Context, username string) (domain.User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, s.bind(`SELECT `+userColumns+` FROM users WHERE username = ?`), strings.ToLower(username)))
}

func (s *Store) FindUserByID(ctx context.Context, id string) (domain.User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, s.bind(`SELECT `+userColumns+` FROM users WHERE id = ?`), id))
}

func (s *Store) SetUserEnabled(ctx context.Context, id string, enabled bool, now time.Time) (domain.User, error) {
	query := `UPDATE users SET enabled = ?, token_version = token_version + 1, updated_at = ? WHERE id = ? RETURNING ` + userColumns
	return s.scanUser(s.db.QueryRowContext(ctx, s.bind(query), enabled, millis(now), id))
}

func (s *Store) SetUserApproval(ctx context.Context, id string, status domain.ApprovalStatus, now time.Time) (domain.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()
	query := `UPDATE users SET approval_status=?,updated_at=? WHERE id=? RETURNING ` + userColumns
	user, err := s.scanUser(tx.QueryRowContext(ctx, s.bind(query), status, millis(now), id))
	if err != nil {
		return domain.User{}, err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM user_group_memberships WHERE user_id=? AND group_id IN (?,?)`), id, domain.PendingUsersGroupID, domain.ApprovedUsersGroupID); err != nil {
		return domain.User{}, err
	}
	groupID := ""
	if status == domain.ApprovalPending {
		groupID = domain.PendingUsersGroupID
	} else if status == domain.ApprovalApproved {
		groupID = domain.ApprovedUsersGroupID
	}
	if groupID != "" {
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO user_group_memberships(group_id,user_id,created_at) VALUES(?,?,?)`), groupID, id, millis(now)); err != nil {
			return domain.User{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) ForceRelogin(ctx context.Context, id string, now time.Time) (domain.User, error) {
	query := `UPDATE users SET token_version = token_version + 1, force_relogin_at = ?, updated_at = ? WHERE id = ? RETURNING ` + userColumns
	return s.scanUser(s.db.QueryRowContext(ctx, s.bind(query), millis(now), millis(now), id))
}

func (s *Store) UpdateUserPassword(ctx context.Context, id, passwordHash string, now time.Time) (domain.User, error) {
	query := `UPDATE users SET password_hash = ?, token_version = token_version + 1, force_relogin_at = ?, updated_at = ? WHERE id = ? RETURNING ` + userColumns
	return s.scanUser(s.db.QueryRowContext(ctx, s.bind(query), passwordHash, millis(now), millis(now), id))
}

func (s *Store) UpdateUser(ctx context.Context, id string, input domain.UserUpdate, now time.Time) (domain.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()
	var currentRole domain.Role
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT role FROM users WHERE id=?`), id).Scan(&currentRole); errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	} else if err != nil {
		return domain.User{}, err
	}
	if currentRole == domain.RoleAdmin && input.Role != domain.RoleAdmin {
		var count int
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM users WHERE role=?`), domain.RoleAdmin).Scan(&count); err != nil {
			return domain.User{}, err
		}
		if count <= 1 {
			return domain.User{}, domain.ErrLastAdmin
		}
	}
	query := `UPDATE users SET username=?,email=?,phone=?,display_name=?,role=?,enabled=?,token_version=token_version+1,force_relogin_at=?,updated_at=?`
	args := []any{strings.ToLower(input.Username), input.Email, input.Phone, input.DisplayName, input.Role, input.Enabled, millis(now), millis(now)}
	if input.PasswordHash != "" {
		query += `,password_hash=?`
		args = append(args, input.PasswordHash)
	}
	query += ` WHERE id=? RETURNING ` + userColumns
	args = append(args, id)
	user, err := s.scanUser(tx.QueryRowContext(ctx, s.bind(query), args...))
	if err != nil {
		return domain.User{}, err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM user_group_memberships WHERE user_id=? AND group_id NOT IN (?,?)`), id, domain.PendingUsersGroupID, domain.ApprovedUsersGroupID); err != nil {
		return domain.User{}, err
	}
	for _, groupID := range input.GroupIDs {
		var kind domain.GroupKind
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT kind FROM groups WHERE id=?`), groupID).Scan(&kind); err != nil || kind != domain.GroupKindUser {
			return domain.User{}, domain.ErrNotFound
		}
		if groupID == domain.PendingUsersGroupID || groupID == domain.ApprovedUsersGroupID {
			continue
		}
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO user_group_memberships(group_id,user_id,created_at) VALUES(?,?,?) ON CONFLICT(group_id,user_id) DO NOTHING`), groupID, id, millis(now)); err != nil {
			return domain.User{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`), millis(now), id); err != nil {
		return domain.User{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role domain.Role
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT role FROM users WHERE id=?`), id).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	}
	if role == domain.RoleAdmin {
		var count int
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM users WHERE role=?`), domain.RoleAdmin).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return domain.ErrLastAdmin
		}
	}
	for _, statement := range []string{
		`DELETE FROM sessions WHERE user_id=?`, `DELETE FROM auth_challenges WHERE user_id=?`, `DELETE FROM oidc_identities WHERE user_id=?`,
		`DELETE FROM mfa_recovery_codes WHERE user_id=?`, `DELETE FROM user_group_memberships WHERE user_id=?`,
		`DELETE FROM address_book_grants WHERE subject_type='user' AND subject_id=?`, `DELETE FROM acl_rules WHERE subject_type='user' AND subject_id=?`,
		`DELETE FROM strategies WHERE scope_type='user' AND scope_id=?`,
	} {
		if _, err = tx.ExecContext(ctx, s.bind(statement), id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_entries WHERE address_book_id IN (SELECT id FROM address_books WHERE owner_user_id=?)`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_tags WHERE address_book_id IN (SELECT id FROM address_books WHERE owner_user_id=?)`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_grants WHERE address_book_id IN (SELECT id FROM address_books WHERE owner_user_id=?)`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_books WHERE owner_user_id=?`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE devices SET owner_user_id='' WHERE owner_user_id=?`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE oidc_auth_requests SET user_id='',link_user_id='' WHERE user_id=? OR link_user_id=?`), id, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, s.bind(`DELETE FROM users WHERE id=?`), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) SetUserTOTP(ctx context.Context, id, encryptedSecret string, enabled bool, now time.Time) (domain.User, error) {
	query := `UPDATE users SET totp_secret=?,totp_enabled=?,token_version=token_version + CASE WHEN totp_enabled <> ? THEN 1 ELSE 0 END,updated_at=? WHERE id=? RETURNING ` + userColumns
	return s.scanUser(s.db.QueryRowContext(ctx, s.bind(query), encryptedSecret, enabled, enabled, millis(now), id))
}

func (s *Store) ReplaceMFARecoveryCodes(ctx context.Context, userID string, hashes []string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM mfa_recovery_codes WHERE user_id=?`), userID); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO mfa_recovery_codes(user_id,code_hash,created_at) VALUES(?,?,?)`), userID, hash, millis(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ConsumeMFARecoveryCode(ctx context.Context, userID, hash string) (bool, error) {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM mfa_recovery_codes WHERE user_id=? AND code_hash=?`), userID, hash)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) CountMFARecoveryCodes(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id=?`), userID).Scan(&count)
	return count, err
}

func (s *Store) CreateAuthChallenge(ctx context.Context, challenge domain.AuthChallenge) error {
	_, _ = s.db.ExecContext(ctx, s.bind(`DELETE FROM auth_challenges WHERE expires_at<=?`), millis(challenge.CreatedAt))
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO auth_challenges(id,user_id,token_version,created_at,expires_at,ip,user_agent,client_device_id,rustdesk_id,client_uuid,platform,client_type,device_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		challenge.ID, challenge.UserID, challenge.TokenVersion, millis(challenge.CreatedAt), millis(challenge.ExpiresAt), challenge.IP, challenge.UserAgent, challenge.ClientDeviceID,
		challenge.RustDeskID, challenge.ClientUUID, challenge.Platform, challenge.ClientType, challenge.DeviceName)
	return err
}

func (s *Store) FindAuthChallenge(ctx context.Context, id string, now time.Time) (domain.AuthChallenge, error) {
	row := s.db.QueryRowContext(ctx, s.bind(`SELECT id,user_id,token_version,created_at,expires_at,ip,user_agent,client_device_id,rustdesk_id,client_uuid,platform,client_type,device_name FROM auth_challenges WHERE id=? AND expires_at>?`), id, millis(now))
	return scanAuthChallenge(row)
}

func (s *Store) ConsumeAuthChallenge(ctx context.Context, id string, now time.Time) (domain.AuthChallenge, error) {
	row := s.db.QueryRowContext(ctx, s.bind(`DELETE FROM auth_challenges WHERE id=? AND expires_at>? RETURNING id,user_id,token_version,created_at,expires_at,ip,user_agent,client_device_id,rustdesk_id,client_uuid,platform,client_type,device_name`), id, millis(now))
	return scanAuthChallenge(row)
}

type rowScanner interface{ Scan(...any) error }

func scanAuthChallenge(row rowScanner) (domain.AuthChallenge, error) {
	var challenge domain.AuthChallenge
	var createdAt, expiresAt int64
	if err := row.Scan(&challenge.ID, &challenge.UserID, &challenge.TokenVersion, &createdAt, &expiresAt, &challenge.IP, &challenge.UserAgent, &challenge.ClientDeviceID,
		&challenge.RustDeskID, &challenge.ClientUUID, &challenge.Platform, &challenge.ClientType, &challenge.DeviceName); errors.Is(err, sql.ErrNoRows) {
		return domain.AuthChallenge{}, domain.ErrNotFound
	} else if err != nil {
		return domain.AuthChallenge{}, err
	}
	challenge.CreatedAt, challenge.ExpiresAt = fromMillis(createdAt), fromMillis(expiresAt)
	return challenge, nil
}

func (s *Store) CreateOIDCAuthRequest(ctx context.Context, value domain.OIDCAuthRequest) error {
	_, _ = s.db.ExecContext(ctx, s.bind(`DELETE FROM oidc_auth_requests WHERE expires_at<=?`), millis(value.CreatedAt))
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO oidc_auth_requests(state,poll_code,provider,verifier,nonce,user_id,link_user_id,error,rustdesk_id,client_uuid,platform,client_type,device_name,ip,user_agent,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		value.State, value.PollCode, value.Provider, value.Verifier, value.Nonce, value.UserID, value.LinkUserID, value.Error, value.RustDeskID, value.ClientUUID, value.Platform, value.ClientType, value.DeviceName, value.IP, value.UserAgent, millis(value.CreatedAt), millis(value.ExpiresAt))
	return err
}

func (s *Store) FindOIDCAuthRequestByState(ctx context.Context, state string, now time.Time) (domain.OIDCAuthRequest, error) {
	return scanOIDCAuthRequest(s.db.QueryRowContext(ctx, s.bind(`SELECT state,poll_code,provider,verifier,nonce,user_id,link_user_id,error,rustdesk_id,client_uuid,platform,client_type,device_name,ip,user_agent,created_at,expires_at FROM oidc_auth_requests WHERE state=? AND expires_at>?`), state, millis(now)))
}

func (s *Store) CompleteOIDCAuthRequest(ctx context.Context, state, userID, errorMessage string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE oidc_auth_requests SET user_id=?,error=? WHERE state=? AND user_id='' AND error=''`), userID, errorMessage, state)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ConsumeOIDCAuthRequest(ctx context.Context, pollCode, rustDeskID, clientUUID string, now time.Time) (domain.OIDCAuthRequest, error) {
	return scanOIDCAuthRequest(s.db.QueryRowContext(ctx, s.bind(`DELETE FROM oidc_auth_requests WHERE poll_code=? AND rustdesk_id=? AND client_uuid=? AND link_user_id='' AND expires_at>? AND (user_id<>'' OR error<>'') RETURNING state,poll_code,provider,verifier,nonce,user_id,link_user_id,error,rustdesk_id,client_uuid,platform,client_type,device_name,ip,user_agent,created_at,expires_at`), pollCode, rustDeskID, clientUUID, millis(now)))
}

func (s *Store) ConsumeOIDCLinkRequest(ctx context.Context, pollCode, userID string, now time.Time) (domain.OIDCAuthRequest, error) {
	return scanOIDCAuthRequest(s.db.QueryRowContext(ctx, s.bind(`DELETE FROM oidc_auth_requests WHERE poll_code=? AND link_user_id=? AND expires_at>? AND (user_id<>'' OR error<>'') RETURNING state,poll_code,provider,verifier,nonce,user_id,link_user_id,error,rustdesk_id,client_uuid,platform,client_type,device_name,ip,user_agent,created_at,expires_at`), pollCode, userID, millis(now)))
}

func scanOIDCAuthRequest(row rowScanner) (domain.OIDCAuthRequest, error) {
	var value domain.OIDCAuthRequest
	var created, expires int64
	err := row.Scan(&value.State, &value.PollCode, &value.Provider, &value.Verifier, &value.Nonce, &value.UserID, &value.LinkUserID, &value.Error, &value.RustDeskID, &value.ClientUUID, &value.Platform, &value.ClientType, &value.DeviceName, &value.IP, &value.UserAgent, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.ErrNotFound
	}
	if err != nil {
		return value, err
	}
	value.CreatedAt, value.ExpiresAt = fromMillis(created), fromMillis(expires)
	return value, nil
}

func (s *Store) FindOIDCIdentity(ctx context.Context, provider, subject string) (domain.OIDCIdentity, error) {
	var value domain.OIDCIdentity
	var created int64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT provider,subject,user_id,email,created_at FROM oidc_identities WHERE provider=? AND subject=?`), provider, subject).Scan(&value.Provider, &value.Subject, &value.UserID, &value.Email, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.ErrNotFound
	}
	value.CreatedAt = fromMillis(created)
	return value, err
}
func (s *Store) CreateOIDCIdentity(ctx context.Context, value domain.OIDCIdentity) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO oidc_identities(provider,subject,user_id,email,created_at) VALUES(?,?,?,?,?)`), value.Provider, value.Subject, value.UserID, value.Email, millis(value.CreatedAt))
	return err
}

func (s *Store) ListOIDCIdentities(ctx context.Context, userID string) ([]domain.OIDCIdentity, error) {
	query, args := `SELECT provider,subject,user_id,email,created_at FROM oidc_identities`, []any{}
	if userID != "" {
		query, args = query+` WHERE user_id=?`, append(args, userID)
	}
	rows, err := s.db.QueryContext(ctx, s.bind(query+` ORDER BY provider,email,subject`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.OIDCIdentity, 0)
	for rows.Next() {
		var value domain.OIDCIdentity
		var created int64
		if err = rows.Scan(&value.Provider, &value.Subject, &value.UserID, &value.Email, &created); err != nil {
			return nil, err
		}
		value.CreatedAt = fromMillis(created)
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) DeleteOIDCIdentity(ctx context.Context, provider, subject, userID string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM oidc_identities WHERE provider=? AND subject=? AND user_id=?`), provider, subject, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateLastLogin(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`), millis(at), millis(at), id)
	return err
}

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO sessions
        (id, user_id, created_at, expires_at, last_seen_at, ip, user_agent, client_device_id)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), session.ID, session.UserID, millis(session.CreatedAt),
		millis(session.ExpiresAt), millis(session.LastSeenAt), session.IP, session.UserAgent, session.ClientDeviceID)
	return err
}

func (s *Store) FindSession(ctx context.Context, id string) (domain.Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, s.bind(`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`), id))
}

func (s *Store) TouchSession(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`UPDATE sessions SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL`), millis(at), id)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, id string, at time.Time) (domain.Session, error) {
	query := `UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL RETURNING ` + sessionColumns
	return scanSession(s.db.QueryRowContext(ctx, s.bind(query), millis(at), id))
}

func (s *Store) RevokeUserSessions(ctx context.Context, userID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`), millis(at), userID)
	return err
}

func (s *Store) ListAuthState(ctx context.Context, now time.Time) ([]domain.User, []domain.Session, error) {
	userRows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users`)
	if err != nil {
		return nil, nil, err
	}
	defer userRows.Close()
	users := make([]domain.User, 0)
	for userRows.Next() {
		user, err := s.scanUser(userRows)
		if err != nil {
			return nil, nil, err
		}
		users = append(users, user)
	}
	if err := userRows.Err(); err != nil {
		return nil, nil, err
	}
	sessionRows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+sessionColumns+` FROM sessions WHERE expires_at > ?`), millis(now))
	if err != nil {
		return nil, nil, err
	}
	defer sessionRows.Close()
	sessions := make([]domain.Session, 0)
	for sessionRows.Next() {
		session, err := scanSession(sessionRows)
		if err != nil {
			return nil, nil, err
		}
		sessions = append(sessions, session)
	}
	return users, sessions, sessionRows.Err()
}

func (s *Store) AppendAudit(ctx context.Context, event domain.AuditEvent) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO audit_events
        (id, occurred_at, type, actor_user_id, actor_session_id, controller_device_id, target_rustdesk_id, ip, result, reason, metadata)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`), event.ID, millis(event.OccurredAt), event.Type,
		event.ActorUserID, event.ActorSessionID, event.ControllerDevice, event.TargetRustDeskID,
		event.IP, event.Result, event.Reason, string(metadata))
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id, occurred_at, type, actor_user_id,
        actor_session_id, controller_device_id, target_rustdesk_id, ip, result, reason, metadata
        FROM audit_events ORDER BY occurred_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var event domain.AuditEvent
		var occurred int64
		var metadata string
		if err := rows.Scan(&event.ID, &occurred, &event.Type, &event.ActorUserID, &event.ActorSessionID,
			&event.ControllerDevice, &event.TargetRustDeskID, &event.IP, &event.Result, &event.Reason, &metadata); err != nil {
			return nil, err
		}
		event.OccurredAt = fromMillis(occurred)
		_ = json.Unmarshal([]byte(metadata), &event.Metadata)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) QueryAudit(ctx context.Context, query domain.AuditQuery) (domain.AuditPage, error) {
	if query.Limit < 1 || query.Limit > 500 {
		query.Limit = 100
	}
	if query.Offset < 0 || query.Offset > 10_000_000 {
		query.Offset = 0
	}
	where, arguments := auditWhere(query)
	var total int64
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM audit_events`+where), arguments...).Scan(&total); err != nil {
		return domain.AuditPage{}, err
	}
	arguments = append(arguments, query.Limit, query.Offset)
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,occurred_at,type,actor_user_id,actor_session_id,controller_device_id,target_rustdesk_id,ip,result,reason,metadata FROM audit_events`+where+` ORDER BY occurred_at DESC,id DESC LIMIT ? OFFSET ?`), arguments...)
	if err != nil {
		return domain.AuditPage{}, err
	}
	defer rows.Close()
	events := make([]domain.AuditEvent, 0, query.Limit)
	for rows.Next() {
		event, scanErr := scanAuditEvent(rows)
		if scanErr != nil {
			return domain.AuditPage{}, scanErr
		}
		events = append(events, event)
	}
	return domain.AuditPage{Events: events, Total: total, Limit: query.Limit, Offset: query.Offset}, rows.Err()
}

func (s *Store) AuditSummary(ctx context.Context, query domain.AuditQuery) (domain.AuditSummary, error) {
	where, arguments := auditWhere(query)
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT type,result,COUNT(*) FROM audit_events`+where+` GROUP BY type,result`), arguments...)
	if err != nil {
		return domain.AuditSummary{}, err
	}
	defer rows.Close()
	result := domain.AuditSummary{ByType: map[string]int64{}, ByResult: map[string]int64{}}
	for rows.Next() {
		var eventType, eventResult string
		var count int64
		if err = rows.Scan(&eventType, &eventResult, &count); err != nil {
			return domain.AuditSummary{}, err
		}
		result.Total += count
		result.ByType[eventType] += count
		result.ByResult[eventResult] += count
		if eventType == "connection_allowed" {
			result.AllowedConnections += count
		}
		if eventType == "connection_denied" {
			result.DeniedConnections += count
		}
		if eventType == "login_failed" {
			result.FailedLogins += count
		}
	}
	return result, rows.Err()
}

type auditScanner interface {
	Scan(...any) error
}

func scanAuditEvent(row auditScanner) (domain.AuditEvent, error) {
	var event domain.AuditEvent
	var occurred int64
	var metadata string
	if err := row.Scan(&event.ID, &occurred, &event.Type, &event.ActorUserID, &event.ActorSessionID, &event.ControllerDevice, &event.TargetRustDeskID, &event.IP, &event.Result, &event.Reason, &metadata); err != nil {
		return domain.AuditEvent{}, err
	}
	event.OccurredAt = fromMillis(occurred)
	_ = json.Unmarshal([]byte(metadata), &event.Metadata)
	return event, nil
}

func auditWhere(query domain.AuditQuery) (string, []any) {
	clauses, arguments := make([]string, 0, 8), make([]any, 0, 8)
	add := func(clause string, value any) { clauses, arguments = append(clauses, clause), append(arguments, value) }
	if query.Type != "" {
		add("type=?", query.Type)
	}
	if query.Result != "" {
		add("result=?", query.Result)
	}
	if query.ActorUserID != "" {
		add("actor_user_id=?", query.ActorUserID)
	}
	if query.TargetID != "" {
		add("target_rustdesk_id=?", query.TargetID)
	}
	if query.IP != "" {
		add("ip=?", query.IP)
	}
	if query.From != nil {
		add("occurred_at>=?", millis(*query.From))
	}
	if query.To != nil {
		add("occurred_at<=?", millis(*query.To))
	}
	if query.Search != "" {
		pattern := "%" + strings.ToLower(query.Search) + "%"
		clauses = append(clauses, "(LOWER(type) LIKE ? OR LOWER(actor_user_id) LIKE ? OR LOWER(controller_device_id) LIKE ? OR LOWER(target_rustdesk_id) LIKE ? OR LOWER(ip) LIKE ? OR LOWER(result) LIKE ? OR LOWER(reason) LIKE ?)")
		for range 7 {
			arguments = append(arguments, pattern)
		}
	}
	if len(clauses) == 0 {
		return "", arguments
	}
	return " WHERE " + strings.Join(clauses, " AND "), arguments
}

func (s *Store) ListSessions(ctx context.Context, now time.Time) ([]domain.Session, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+sessionColumns+` FROM sessions
        WHERE expires_at > ? ORDER BY last_seen_at DESC`), millis(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]domain.Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *Store) QuerySessions(ctx context.Context, query domain.SessionQuery) (domain.SessionPage, error) {
	if query.Limit < 1 || query.Limit > 500 {
		query.Limit = 100
	}
	if query.Offset < 0 || query.Offset > 10_000_000 {
		query.Offset = 0
	}
	if query.Now.IsZero() {
		query.Now = time.Now().UTC()
	}
	where, arguments := sessionWhere(query)
	base := ` FROM sessions s JOIN users u ON u.id=s.user_id`
	var total int64
	if err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*)`+base+where), arguments...).Scan(&total); err != nil {
		return domain.SessionPage{}, err
	}
	arguments = append(arguments, query.Limit, query.Offset)
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT s.`+strings.ReplaceAll(sessionColumns, ", ", ",s.")+`,u.username,u.display_name,u.enabled`+base+where+` ORDER BY s.last_seen_at DESC,s.id DESC LIMIT ? OFFSET ?`), arguments...)
	if err != nil {
		return domain.SessionPage{}, err
	}
	defer rows.Close()
	values := make([]domain.SessionRecord, 0, query.Limit)
	for rows.Next() {
		var value domain.SessionRecord
		var created, expires, lastSeen int64
		var revoked sql.NullInt64
		if err = rows.Scan(&value.ID, &value.UserID, &created, &expires, &revoked, &lastSeen, &value.IP, &value.UserAgent, &value.ClientDeviceID, &value.Username, &value.DisplayName, &value.UserEnabled); err != nil {
			return domain.SessionPage{}, err
		}
		value.CreatedAt, value.ExpiresAt, value.LastSeenAt = fromMillis(created), fromMillis(expires), fromMillis(lastSeen)
		if revoked.Valid {
			at := fromMillis(revoked.Int64)
			value.RevokedAt = &at
			value.Status = "revoked"
		} else if !query.Now.Before(value.ExpiresAt) {
			value.Status = "expired"
		} else {
			value.Status = "active"
		}
		values = append(values, value)
	}
	return domain.SessionPage{Sessions: values, Total: total, Limit: query.Limit, Offset: query.Offset}, rows.Err()
}

func (s *Store) SessionSummary(ctx context.Context, now time.Time) (domain.SessionSummary, error) {
	var result domain.SessionSummary
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN revoked_at IS NULL AND expires_at>? THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN revoked_at IS NOT NULL THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN revoked_at IS NULL AND expires_at<=? THEN 1 ELSE 0 END),0) FROM sessions`), millis(now), millis(now)).Scan(&result.Total, &result.Active, &result.Revoked, &result.Expired)
	return result, err
}

func sessionWhere(query domain.SessionQuery) (string, []any) {
	clauses, arguments := make([]string, 0, 4), make([]any, 0, 8)
	switch query.Status {
	case "active":
		clauses = append(clauses, "s.revoked_at IS NULL AND s.expires_at>?")
		arguments = append(arguments, millis(query.Now))
	case "revoked":
		clauses = append(clauses, "s.revoked_at IS NOT NULL")
	case "expired":
		clauses = append(clauses, "s.revoked_at IS NULL AND s.expires_at<=?")
		arguments = append(arguments, millis(query.Now))
	}
	if query.UserID != "" {
		clauses = append(clauses, "s.user_id=?")
		arguments = append(arguments, query.UserID)
	}
	if query.Search != "" {
		pattern := "%" + strings.ToLower(query.Search) + "%"
		clauses = append(clauses, "(LOWER(u.username) LIKE ? OR LOWER(u.display_name) LIKE ? OR LOWER(s.user_id) LIKE ? OR LOWER(s.client_device_id) LIKE ? OR LOWER(s.ip) LIKE ? OR LOWER(s.user_agent) LIKE ?)")
		for range 6 {
			arguments = append(arguments, pattern)
		}
	}
	if len(clauses) == 0 {
		return "", arguments
	}
	return " WHERE " + strings.Join(clauses, " AND "), arguments
}

func (s *Store) UpsertDevice(ctx context.Context, device domain.Device) error {
	if device.Tags == nil {
		device.Tags = []string{}
	}
	tags, err := json.Marshal(device.Tags)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO devices
		(rustdesk_id, client_uuid, hostname, alias, platform, version, cpu, memory, os_username, last_seen_ip, online, last_seen, owner_user_id, group_id, tags, public_key, deployed, deployed_by, deployed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(rustdesk_id) DO UPDATE SET
        client_uuid = CASE WHEN devices.deployed THEN devices.client_uuid ELSE excluded.client_uuid END,
        hostname = CASE WHEN excluded.hostname = '' THEN devices.hostname ELSE excluded.hostname END,
        platform = CASE WHEN excluded.platform = '' THEN devices.platform ELSE excluded.platform END,
        version = CASE WHEN excluded.version = '' THEN devices.version ELSE excluded.version END,
        cpu = CASE WHEN excluded.cpu = '' THEN devices.cpu ELSE excluded.cpu END,
        memory = CASE WHEN excluded.memory = '' THEN devices.memory ELSE excluded.memory END,
        os_username = CASE WHEN excluded.os_username = '' THEN devices.os_username ELSE excluded.os_username END,
        last_seen_ip = CASE WHEN excluded.last_seen_ip = '' THEN devices.last_seen_ip ELSE excluded.last_seen_ip END,
        online = excluded.online, last_seen = excluded.last_seen,
		owner_user_id = CASE WHEN excluded.owner_user_id = '' THEN devices.owner_user_id ELSE excluded.owner_user_id END,
		public_key = CASE WHEN excluded.public_key = '' THEN devices.public_key ELSE excluded.public_key END,
		deployed = devices.deployed OR excluded.deployed,
		deployed_by = CASE WHEN excluded.deployed_by = '' THEN devices.deployed_by ELSE excluded.deployed_by END,
		deployed_at = CASE WHEN excluded.deployed_at IS NULL THEN devices.deployed_at ELSE excluded.deployed_at END`),
		device.RustDeskID, device.ClientUUID, device.Hostname, device.Alias, device.Platform, device.Version,
		device.CPU, device.Memory, device.Username, device.LastSeenIP, device.Online, millis(device.LastSeen),
		device.OwnerUserID, device.GroupID, string(tags), device.PublicKey, device.Deployed, device.DeployedBy, nullableMillis(device.DeployedAt), millis(device.CreatedAt))
	return err
}

func (s *Store) ListDevices(ctx context.Context) ([]domain.Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rustdesk_id, client_uuid, hostname, alias, platform,
		version, cpu, memory, os_username, last_seen_ip, online, last_seen, owner_user_id, group_id, tags, public_key, deployed, deployed_by, deployed_at, created_at, archived_at FROM devices ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := make([]domain.Device, 0)
	for rows.Next() {
		var device domain.Device
		var lastSeen, created int64
		var deployedAt, archivedAt sql.NullInt64
		var tags string
		if err := rows.Scan(&device.RustDeskID, &device.ClientUUID, &device.Hostname, &device.Alias,
			&device.Platform, &device.Version, &device.CPU, &device.Memory, &device.Username,
			&device.LastSeenIP, &device.Online, &lastSeen, &device.OwnerUserID,
			&device.GroupID, &tags, &device.PublicKey, &device.Deployed, &device.DeployedBy, &deployedAt, &created, &archivedAt); err != nil {
			return nil, err
		}
		device.LastSeen, device.CreatedAt = fromMillis(lastSeen), fromMillis(created)
		if deployedAt.Valid {
			device.DeployedAt = fromMillis(deployedAt.Int64)
		}
		if archivedAt.Valid {
			value := fromMillis(archivedAt.Int64)
			device.ArchivedAt = &value
		}
		device.Online = time.Since(device.LastSeen) < deviceOnlineTTL()
		_ = json.Unmarshal([]byte(tags), &device.Tags)
		if device.Tags == nil {
			device.Tags = []string{}
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

// DeviceAuditLabel keeps connection-audit enrichment on a single indexed lookup
// instead of loading the complete inventory for every rendezvous decision.
func (s *Store) DeviceAuditLabel(ctx context.Context, rustDeskID string) (string, string, error) {
	var hostname, alias string
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT hostname,alias FROM devices WHERE rustdesk_id=?`), rustDeskID).Scan(&hostname, &alias)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", domain.ErrNotFound
	}
	return hostname, alias, err
}

// AuditActorByDevice attributes an endpoint-originated client audit report to
// the most recently active authenticated session of the controlling RustDesk ID.
func (s *Store) AuditActorByDevice(ctx context.Context, rustDeskID string, now time.Time) (string, string, string, string, error) {
	var userID, sessionID, username, displayName string
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT u.id,s.id,u.username,u.display_name
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.revoked_at IS NULL AND s.expires_at>? AND u.enabled=?
		AND (s.client_device_id=? OR s.client_device_id=(SELECT client_uuid FROM devices WHERE rustdesk_id=?))
		ORDER BY s.last_seen_at DESC LIMIT 1`), millis(now), true, rustDeskID, rustDeskID).Scan(&userID, &sessionID, &username, &displayName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", "", domain.ErrNotFound
	}
	return userID, sessionID, username, displayName, err
}

func deviceOnlineTTL() time.Duration {
	value := os.Getenv("ART_DEVICE_ONLINE_TTL")
	if value == "" {
		return 180 * time.Second
	}
	if parsed, err := time.ParseDuration(value); err == nil && parsed >= 30*time.Second {
		return parsed
	}
	return 180 * time.Second
}

func (s *Store) UpdateDeviceManagement(ctx context.Context, id, alias, groupID string, tags []string) (domain.Device, error) {
	encoded, err := json.Marshal(tags)
	if err != nil {
		return domain.Device{}, err
	}
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE devices SET alias=?,group_id=?,tags=? WHERE rustdesk_id=?`), alias, groupID, string(encoded), id)
	if err != nil {
		return domain.Device{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return domain.Device{}, domain.ErrNotFound
	}
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return domain.Device{}, err
	}
	for _, device := range devices {
		if device.RustDeskID == id {
			return device, nil
		}
	}
	return domain.Device{}, domain.ErrNotFound
}

func (s *Store) BulkUpdateDevices(ctx context.Context, ids []string, groupID *string, addTags, removeTags []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var encoded string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT tags FROM devices WHERE rustdesk_id=?`), id).Scan(&encoded); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}
		var current []string
		_ = json.Unmarshal([]byte(encoded), &current)
		removed := make(map[string]bool, len(removeTags))
		for _, tag := range removeTags {
			removed[strings.TrimSpace(tag)] = true
		}
		seen := make(map[string]bool, len(current)+len(addTags))
		merged := make([]string, 0, len(current)+len(addTags))
		for _, tag := range append(current, addTags...) {
			tag = strings.TrimSpace(tag)
			if tag != "" && !removed[tag] && !seen[tag] {
				seen[tag] = true
				merged = append(merged, tag)
			}
		}
		encodedTags, marshalErr := json.Marshal(merged)
		if marshalErr != nil {
			return marshalErr
		}
		if groupID == nil {
			_, err = tx.ExecContext(ctx, s.bind(`UPDATE devices SET tags=? WHERE rustdesk_id=?`), string(encodedTags), id)
		} else {
			_, err = tx.ExecContext(ctx, s.bind(`UPDATE devices SET group_id=?,tags=? WHERE rustdesk_id=?`), *groupID, string(encodedTags), id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetDeviceArchived(ctx context.Context, id string, archived bool, now time.Time) (domain.Device, error) {
	var archivedAt any
	if archived {
		archivedAt = millis(now)
	}
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE devices SET archived_at=? WHERE rustdesk_id=?`), archivedAt, id)
	if err != nil {
		return domain.Device{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return domain.Device{}, domain.ErrNotFound
	}
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return domain.Device{}, err
	}
	for _, device := range devices {
		if device.RustDeskID == id {
			return device, nil
		}
	}
	return domain.Device{}, domain.ErrNotFound
}

func (s *Store) DeleteArchivedDevice(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM devices WHERE rustdesk_id=? AND archived_at IS NOT NULL`), id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) ImportDeviceManagement(ctx context.Context, values []domain.DeviceManagementImport) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := s.bind(`UPDATE devices SET alias=?,group_id=?,tags=? WHERE rustdesk_id=? AND archived_at IS NULL`)
	for _, value := range values {
		tags, marshalErr := json.Marshal(value.Tags)
		if marshalErr != nil {
			return marshalErr
		}
		result, execErr := tx.ExecContext(ctx, query, value.Alias, value.GroupID, string(tags), value.RustDeskID)
		if execErr != nil {
			return execErr
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return domain.ErrNotFound
		}
	}
	return tx.Commit()
}

func (s *Store) CreateGroup(ctx context.Context, group domain.Group) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO groups
        (id, name, description, kind, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`),
		group.ID, group.Name, group.Description, group.Kind, millis(group.CreatedAt), millis(group.UpdatedAt))
	return err
}

func (s *Store) ListGroups(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, kind, created_at, updated_at
        FROM groups ORDER BY kind, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]domain.Group, 0)
	for rows.Next() {
		var group domain.Group
		var created, updated int64
		if err := rows.Scan(&group.ID, &group.Name, &group.Description, &group.Kind, &created, &updated); err != nil {
			return nil, err
		}
		group.CreatedAt, group.UpdatedAt = fromMillis(created), fromMillis(updated)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) FindGroupByID(ctx context.Context, id string) (domain.Group, error) {
	var group domain.Group
	var created, updated int64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT id,name,description,kind,created_at,updated_at FROM groups WHERE id=?`), id).
		Scan(&group.ID, &group.Name, &group.Description, &group.Kind, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Group{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Group{}, err
	}
	group.CreatedAt, group.UpdatedAt = fromMillis(created), fromMillis(updated)
	return group, nil
}

func (s *Store) UpdateGroup(ctx context.Context, group domain.Group) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE groups SET name=?,description=?,updated_at=? WHERE id=? AND kind=?`),
		group.Name, group.Description, millis(group.UpdatedAt), group.ID, group.Kind)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT 1 FROM groups WHERE id=?`), id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM user_group_memberships WHERE group_id=?`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_grants WHERE subject_type='user_group' AND subject_id=?`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM acl_rules WHERE (subject_type='user_group' AND subject_id=?) OR (target_type='device_group' AND target_id=?)`), id, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM strategies WHERE (scope_type='user_group' OR scope_type='device_group') AND scope_id=?`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE devices SET group_id='' WHERE group_id=?`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM groups WHERE id=?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetUserGroupMember(ctx context.Context, groupID, userID string, active bool) error {
	if !active {
		_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM user_group_memberships WHERE group_id=? AND user_id=?`), groupID, userID)
		return err
	}
	query := `INSERT INTO user_group_memberships (group_id,user_id,created_at) VALUES (?,?,?) ON CONFLICT(group_id,user_id) DO NOTHING`
	_, err := s.db.ExecContext(ctx, s.bind(query), groupID, userID, millis(time.Now().UTC()))
	return err
}

func (s *Store) ListUserGroupMemberships(ctx context.Context) ([]domain.UserGroupMembership, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT group_id,user_id FROM user_group_memberships ORDER BY group_id,user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.UserGroupMembership, 0)
	for rows.Next() {
		var value domain.UserGroupMembership
		if err := rows.Scan(&value.GroupID, &value.UserID); err != nil {
			return nil, err
		}
		value.Active = true
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) CreateAddressBook(ctx context.Context, value domain.AddressBook) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO address_books (id,name,kind,owner_user_id,created_at,updated_at) VALUES (?,?,?,?,?,?)`), value.ID, value.Name, value.Kind, value.OwnerUserID, millis(value.CreatedAt), millis(value.UpdatedAt))
	return err
}
func (s *Store) ListAddressBooks(ctx context.Context) ([]domain.AddressBook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,kind,owner_user_id,created_at,updated_at FROM address_books ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AddressBook{}
	for rows.Next() {
		var v domain.AddressBook
		var c, u int64
		if err := rows.Scan(&v.ID, &v.Name, &v.Kind, &v.OwnerUserID, &c, &u); err != nil {
			return nil, err
		}
		v.CreatedAt, v.UpdatedAt = fromMillis(c), fromMillis(u)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) FindAddressBookByID(ctx context.Context, id string) (domain.AddressBook, error) {
	var v domain.AddressBook
	var created, updated int64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT id,name,kind,owner_user_id,created_at,updated_at FROM address_books WHERE id=?`), id).
		Scan(&v.ID, &v.Name, &v.Kind, &v.OwnerUserID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return v, domain.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.CreatedAt, v.UpdatedAt = fromMillis(created), fromMillis(updated)
	return v, nil
}
func (s *Store) UpdateAddressBook(ctx context.Context, v domain.AddressBook) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE address_books SET name=?,kind=?,updated_at=? WHERE id=?`), v.Name, v.Kind, millis(v.UpdatedAt), v.ID)
	return affected(result, err)
}
func (s *Store) DeleteAddressBook(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_entries WHERE address_book_id=?`), id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_tags WHERE address_book_id=?`), id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_grants WHERE address_book_id=?`), id); err != nil {
		_ = tx.Rollback()
		return err
	}
	result, err := tx.ExecContext(ctx, s.bind(`DELETE FROM address_books WHERE id=?`), id)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if count == 0 {
		_ = tx.Rollback()
		return domain.ErrNotFound
	}
	return tx.Commit()
}
func (s *Store) UpsertAddressBookGrant(ctx context.Context, v domain.AddressBookGrant) error {
	query := `INSERT INTO address_book_grants (id,address_book_id,subject_type,subject_id,permission,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?) ON CONFLICT(address_book_id,subject_type,subject_id)
		DO UPDATE SET permission=excluded.permission,updated_at=excluded.updated_at`
	_, err := s.db.ExecContext(ctx, s.bind(query), v.ID, v.AddressBookID, v.SubjectType, v.SubjectID, v.Permission, millis(v.CreatedAt), millis(v.UpdatedAt))
	return err
}
func (s *Store) ListAddressBookGrants(ctx context.Context, bookID string) ([]domain.AddressBookGrant, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,address_book_id,subject_type,subject_id,permission,created_at,updated_at FROM address_book_grants WHERE address_book_id=? ORDER BY subject_type,subject_id`), bookID)
	return scanAddressBookGrants(rows, err)
}
func (s *Store) ListAllAddressBookGrants(ctx context.Context) ([]domain.AddressBookGrant, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,address_book_id,subject_type,subject_id,permission,created_at,updated_at FROM address_book_grants ORDER BY address_book_id,subject_type,subject_id`)
	return scanAddressBookGrants(rows, err)
}
func scanAddressBookGrants(rows *sql.Rows, err error) ([]domain.AddressBookGrant, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.AddressBookGrant, 0)
	for rows.Next() {
		var v domain.AddressBookGrant
		var created, updated int64
		if err := rows.Scan(&v.ID, &v.AddressBookID, &v.SubjectType, &v.SubjectID, &v.Permission, &created, &updated); err != nil {
			return nil, err
		}
		v.CreatedAt, v.UpdatedAt = fromMillis(created), fromMillis(updated)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) DeleteAddressBookGrant(ctx context.Context, bookID, grantID string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM address_book_grants WHERE id=? AND address_book_id=?`), grantID, bookID)
	return affected(result, err)
}
func (s *Store) CreateAddressBookEntry(ctx context.Context, v domain.AddressBookEntry) error {
	tags, _ := json.Marshal(v.Tags)
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO address_book_entries (id,address_book_id,rustdesk_id,alias,username,hostname,platform,folder,favourite,tags,force_relay,rdp_port,rdp_username,login_name,same_server,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`), v.ID, v.AddressBookID, v.RustDeskID, v.Alias, v.Username, v.Hostname, v.Platform, v.Folder, v.Favourite, string(tags), v.ForceRelay, v.RDPPort, v.RDPUsername, v.LoginName, v.SameServer, millis(v.CreatedAt))
	return err
}
func (s *Store) ListAddressBookEntries(ctx context.Context, bookID string) ([]domain.AddressBookEntry, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,address_book_id,rustdesk_id,alias,username,hostname,platform,folder,favourite,tags,force_relay,rdp_port,rdp_username,login_name,same_server,created_at FROM address_book_entries WHERE address_book_id=? ORDER BY favourite DESC,alias,rustdesk_id`), bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AddressBookEntry{}
	for rows.Next() {
		var v domain.AddressBookEntry
		var created int64
		var tags string
		if err := rows.Scan(&v.ID, &v.AddressBookID, &v.RustDeskID, &v.Alias, &v.Username, &v.Hostname, &v.Platform, &v.Folder, &v.Favourite, &tags, &v.ForceRelay, &v.RDPPort, &v.RDPUsername, &v.LoginName, &v.SameServer, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &v.Tags)
		if v.Tags == nil {
			v.Tags = []string{}
		}
		v.CreatedAt = fromMillis(created)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ReplaceAddressBookEntries(ctx context.Context, bookID string, values []domain.AddressBookEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM address_book_entries WHERE address_book_id=?`), bookID); err != nil {
		_ = tx.Rollback()
		return err
	}
	query := s.bind(`INSERT INTO address_book_entries (id,address_book_id,rustdesk_id,alias,username,hostname,platform,folder,favourite,tags,force_relay,rdp_port,rdp_username,login_name,same_server,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	for _, value := range values {
		tags, _ := json.Marshal(value.Tags)
		if _, err = tx.ExecContext(ctx, query, value.ID, bookID, value.RustDeskID, value.Alias, value.Username, value.Hostname, value.Platform, value.Folder, value.Favourite, string(tags), value.ForceRelay, value.RDPPort, value.RDPUsername, value.LoginName, value.SameServer, millis(value.CreatedAt)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) UpdateAddressBookEntry(ctx context.Context, v domain.AddressBookEntry) error {
	tags, _ := json.Marshal(v.Tags)
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE address_book_entries SET rustdesk_id=?,alias=?,username=?,hostname=?,platform=?,folder=?,favourite=?,tags=?,force_relay=?,rdp_port=?,rdp_username=?,login_name=?,same_server=? WHERE id=? AND address_book_id=?`), v.RustDeskID, v.Alias, v.Username, v.Hostname, v.Platform, v.Folder, v.Favourite, string(tags), v.ForceRelay, v.RDPPort, v.RDPUsername, v.LoginName, v.SameServer, v.ID, v.AddressBookID)
	return affected(result, err)
}
func (s *Store) DeleteAddressBookEntry(ctx context.Context, bookID, entryID string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM address_book_entries WHERE id=? AND address_book_id=?`), entryID, bookID)
	return affected(result, err)
}

func (s *Store) CreateAPIToken(ctx context.Context, value domain.APIToken) error {
	scopes, err := json.Marshal(value.Scopes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO api_tokens(id,user_id,name,token_hash,prefix,scopes,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?)`),
		value.ID, value.UserID, value.Name, value.TokenHash, value.Prefix, string(scopes), millis(value.CreatedAt), millis(value.ExpiresAt))
	return err
}

func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]domain.APIToken, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,user_id,name,token_hash,prefix,scopes,created_at,expires_at,last_used_at,revoked_at FROM api_tokens WHERE user_id=? ORDER BY created_at DESC`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.APIToken, 0)
	for rows.Next() {
		value, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) FindAPITokenByHash(ctx context.Context, tokenHash string) (domain.APIToken, error) {
	return scanAPIToken(s.db.QueryRowContext(ctx, s.bind(`SELECT id,user_id,name,token_hash,prefix,scopes,created_at,expires_at,last_used_at,revoked_at FROM api_tokens WHERE token_hash=?`), tokenHash))
}

func (s *Store) TouchAPIToken(ctx context.Context, id string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE api_tokens SET last_used_at=? WHERE id=? AND revoked_at IS NULL`), millis(at), id)
	return affected(result, err)
}

func (s *Store) RevokeAPIToken(ctx context.Context, userID, id string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE api_tokens SET revoked_at=? WHERE id=? AND user_id=? AND revoked_at IS NULL`), millis(at), id, userID)
	return affected(result, err)
}

func scanAPIToken(row scanner) (domain.APIToken, error) {
	var value domain.APIToken
	var scopes string
	var created, expires int64
	var lastUsed, revoked sql.NullInt64
	err := row.Scan(&value.ID, &value.UserID, &value.Name, &value.TokenHash, &value.Prefix, &scopes, &created, &expires, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.APIToken{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.APIToken{}, err
	}
	_ = json.Unmarshal([]byte(scopes), &value.Scopes)
	value.CreatedAt, value.ExpiresAt = fromMillis(created), fromMillis(expires)
	if lastUsed.Valid {
		at := fromMillis(lastUsed.Int64)
		value.LastUsedAt = &at
	}
	if revoked.Valid {
		at := fromMillis(revoked.Int64)
		value.RevokedAt = &at
	}
	return value, nil
}

func (s *Store) CreateWebhook(ctx context.Context, value domain.Webhook) error {
	eventsJSON, _ := json.Marshal(value.Events)
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO webhooks(id,name,url,events,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`), value.ID, value.Name, value.URL, string(eventsJSON), value.Enabled, millis(value.CreatedAt), millis(value.UpdatedAt))
	return err
}

func (s *Store) ListWebhooks(ctx context.Context) ([]domain.Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,url,events,enabled,created_at,updated_at FROM webhooks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.Webhook, 0)
	for rows.Next() {
		value, scanErr := scanWebhook(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) FindWebhookByID(ctx context.Context, id string) (domain.Webhook, error) {
	return scanWebhook(s.db.QueryRowContext(ctx, s.bind(`SELECT id,name,url,events,enabled,created_at,updated_at FROM webhooks WHERE id=?`), id))
}

func (s *Store) UpdateWebhook(ctx context.Context, value domain.Webhook) error {
	eventsJSON, _ := json.Marshal(value.Events)
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE webhooks SET name=?,url=?,events=?,enabled=?,updated_at=? WHERE id=?`), value.Name, value.URL, string(eventsJSON), value.Enabled, millis(value.UpdatedAt), value.ID)
	return affected(result, err)
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM webhooks WHERE id=?`), id)
	return affected(result, err)
}

func scanWebhook(row scanner) (domain.Webhook, error) {
	var value domain.Webhook
	var eventsJSON string
	var created, updated int64
	if err := row.Scan(&value.ID, &value.Name, &value.URL, &eventsJSON, &value.Enabled, &created, &updated); errors.Is(err, sql.ErrNoRows) {
		return domain.Webhook{}, domain.ErrNotFound
	} else if err != nil {
		return domain.Webhook{}, err
	}
	if err := json.Unmarshal([]byte(eventsJSON), &value.Events); err != nil {
		return domain.Webhook{}, err
	}
	value.CreatedAt, value.UpdatedAt = fromMillis(created), fromMillis(updated)
	return value, nil
}

func (s *Store) CreateWebhookDelivery(ctx context.Context, value domain.WebhookDelivery) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO webhook_deliveries(id,webhook_id,event_type,payload,status,attempts,response_code,last_error,next_attempt,created_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`), value.ID, value.WebhookID, value.EventType, value.Payload, value.Status, value.Attempts, value.ResponseCode, value.LastError, millis(value.NextAttempt), millis(value.CreatedAt), nullableTimePtrMillis(value.DeliveredAt))
	return err
}

func (s *Store) ListWebhookDeliveries(ctx context.Context, webhookID string, limit int) ([]domain.WebhookDelivery, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,webhook_id,event_type,payload,status,attempts,response_code,last_error,next_attempt,created_at,delivered_at FROM webhook_deliveries WHERE webhook_id=? ORDER BY created_at DESC LIMIT ?`), webhookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebhookDeliveries(rows)
}

func (s *Store) ListDueWebhookDeliveries(ctx context.Context, now time.Time, limit int) ([]domain.WebhookDelivery, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,webhook_id,event_type,payload,status,attempts,response_code,last_error,next_attempt,created_at,delivered_at FROM webhook_deliveries WHERE status='pending' AND next_attempt<=? ORDER BY next_attempt LIMIT ?`), millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWebhookDeliveries(rows)
}

func scanWebhookDeliveries(rows *sql.Rows) ([]domain.WebhookDelivery, error) {
	values := make([]domain.WebhookDelivery, 0)
	for rows.Next() {
		var value domain.WebhookDelivery
		var nextAttempt, created int64
		var delivered sql.NullInt64
		if err := rows.Scan(&value.ID, &value.WebhookID, &value.EventType, &value.Payload, &value.Status, &value.Attempts, &value.ResponseCode, &value.LastError, &nextAttempt, &created, &delivered); err != nil {
			return nil, err
		}
		value.NextAttempt, value.CreatedAt = fromMillis(nextAttempt), fromMillis(created)
		if delivered.Valid {
			at := fromMillis(delivered.Int64)
			value.DeliveredAt = &at
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) UpdateWebhookDelivery(ctx context.Context, value domain.WebhookDelivery) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE webhook_deliveries SET status=?,attempts=?,response_code=?,last_error=?,next_attempt=?,delivered_at=? WHERE id=?`), value.Status, value.Attempts, value.ResponseCode, value.LastError, millis(value.NextAttempt), nullableTimePtrMillis(value.DeliveredAt), value.ID)
	return affected(result, err)
}

func (s *Store) CreateNotification(ctx context.Context, value domain.Notification) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO notifications(id,type,severity,title,message,resource,created_at,read_at) VALUES(?,?,?,?,?,?,?,?)`), value.ID, value.Type, value.Severity, value.Title, value.Message, value.Resource, millis(value.CreatedAt), nullableTimePtrMillis(value.ReadAt))
	return err
}

func (s *Store) ListNotifications(ctx context.Context, limit int, unreadOnly bool) ([]domain.Notification, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	query := `SELECT id,type,severity,title,message,resource,created_at,read_at FROM notifications`
	if unreadOnly {
		query += ` WHERE read_at IS NULL`
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, s.bind(query), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.Notification, 0)
	for rows.Next() {
		var value domain.Notification
		var created int64
		var read sql.NullInt64
		if err = rows.Scan(&value.ID, &value.Type, &value.Severity, &value.Title, &value.Message, &value.Resource, &created, &read); err != nil {
			return nil, err
		}
		value.CreatedAt = fromMillis(created)
		if read.Valid {
			at := fromMillis(read.Int64)
			value.ReadAt = &at
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) MarkNotificationRead(ctx context.Context, id string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE notifications SET read_at=? WHERE id=? AND read_at IS NULL`), millis(at), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		var exists int
		if scanErr := s.db.QueryRowContext(ctx, s.bind(`SELECT 1 FROM notifications WHERE id=?`), id).Scan(&exists); errors.Is(scanErr, sql.ErrNoRows) {
			return domain.ErrNotFound
		} else if scanErr != nil {
			return scanErr
		}
	}
	return nil
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`UPDATE notifications SET read_at=? WHERE read_at IS NULL`), millis(at))
	return err
}

func nullableTimePtrMillis(value *time.Time) any {
	if value == nil {
		return nil
	}
	return millis(*value)
}

func (s *Store) CreateClientProfile(ctx context.Context, value domain.ClientProfile) error {
	settingsJSON, err := json.Marshal(value.Settings)
	if err != nil {
		return err
	}
	brandingJSON, err := json.Marshal(value.Branding)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO client_profiles(id,name,description,platform,settings,branding,version,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`), value.ID, value.Name, value.Description, value.Platform, string(settingsJSON), string(brandingJSON), value.Version, value.Enabled, millis(value.CreatedAt), millis(value.UpdatedAt))
	return err
}
func (s *Store) ListClientProfiles(ctx context.Context) ([]domain.ClientProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,platform,settings,branding,version,enabled,created_at,updated_at FROM client_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.ClientProfile, 0)
	for rows.Next() {
		value, scanErr := scanClientProfile(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s *Store) FindClientProfileByID(ctx context.Context, id string) (domain.ClientProfile, error) {
	return scanClientProfile(s.db.QueryRowContext(ctx, s.bind(`SELECT id,name,description,platform,settings,branding,version,enabled,created_at,updated_at FROM client_profiles WHERE id=?`), id))
}
func scanClientProfile(row scanner) (domain.ClientProfile, error) {
	var value domain.ClientProfile
	var settingsJSON, brandingJSON string
	var created, updated int64
	err := row.Scan(&value.ID, &value.Name, &value.Description, &value.Platform, &settingsJSON, &brandingJSON, &value.Version, &value.Enabled, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ClientProfile{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ClientProfile{}, err
	}
	if json.Unmarshal([]byte(settingsJSON), &value.Settings) != nil || json.Unmarshal([]byte(brandingJSON), &value.Branding) != nil {
		return domain.ClientProfile{}, errors.New("invalid client profile data")
	}
	value.CreatedAt, value.UpdatedAt = fromMillis(created), fromMillis(updated)
	return value, nil
}
func (s *Store) UpdateClientProfile(ctx context.Context, value domain.ClientProfile) error {
	settingsJSON, err := json.Marshal(value.Settings)
	if err != nil {
		return err
	}
	brandingJSON, err := json.Marshal(value.Branding)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE client_profiles SET name=?,description=?,platform=?,settings=?,branding=?,version=?,enabled=?,updated_at=? WHERE id=?`), value.Name, value.Description, value.Platform, string(settingsJSON), string(brandingJSON), value.Version, value.Enabled, millis(value.UpdatedAt), value.ID)
	return affected(result, err)
}
func (s *Store) DeleteClientProfile(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM client_profile_assignments WHERE profile_id=?`), id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, s.bind(`DELETE FROM client_profiles WHERE id=?`), id)
	if err = affected(result, err); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CreateClientProfileAssignment(ctx context.Context, value domain.ClientProfileAssignment) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO client_profile_assignments(id,profile_id,scope_type,scope_id,priority,created_at) VALUES(?,?,?,?,?,?)`), value.ID, value.ProfileID, value.ScopeType, value.ScopeID, value.Priority, millis(value.CreatedAt))
	return err
}
func (s *Store) ListClientProfileAssignments(ctx context.Context) ([]domain.ClientProfileAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,profile_id,scope_type,scope_id,priority,created_at FROM client_profile_assignments ORDER BY priority,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.ClientProfileAssignment, 0)
	for rows.Next() {
		var value domain.ClientProfileAssignment
		var created int64
		if err = rows.Scan(&value.ID, &value.ProfileID, &value.ScopeType, &value.ScopeID, &value.Priority, &created); err != nil {
			return nil, err
		}
		value.CreatedAt = fromMillis(created)
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s *Store) DeleteClientProfileAssignment(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM client_profile_assignments WHERE id=?`), id)
	return affected(result, err)
}
func (s *Store) CreateClientBuild(ctx context.Context, value domain.ClientBuild) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO client_builds(id,profile_id,target_os,architecture,format,status,artifact_name,media_type,sha256,error,artifact,created_by,worker_id,attempts,created_at,started_at,lease_until,completed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`), value.ID, value.ProfileID, value.TargetOS, value.Architecture, value.Format, value.Status, value.ArtifactName, value.MediaType, value.SHA256, value.Error, value.Artifact, value.CreatedBy, value.WorkerID, value.Attempts, millis(value.CreatedAt), nullableTimePtrMillis(value.StartedAt), nullableTimePtrMillis(value.LeaseUntil), nullableTimePtrMillis(value.CompletedAt))
	return err
}
func (s *Store) ListClientBuilds(ctx context.Context, limit int) ([]domain.ClientBuild, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,profile_id,target_os,architecture,format,status,artifact_name,media_type,sha256,error,artifact,created_by,worker_id,attempts,created_at,started_at,lease_until,completed_at FROM client_builds ORDER BY created_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.ClientBuild, 0)
	for rows.Next() {
		value, scanErr := scanClientBuild(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s *Store) FindClientBuildByID(ctx context.Context, id string) (domain.ClientBuild, error) {
	return scanClientBuild(s.db.QueryRowContext(ctx, s.bind(`SELECT id,profile_id,target_os,architecture,format,status,artifact_name,media_type,sha256,error,artifact,created_by,worker_id,attempts,created_at,started_at,lease_until,completed_at FROM client_builds WHERE id=?`), id))
}
func (s *Store) UpdateClientBuild(ctx context.Context, value domain.ClientBuild) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE client_builds SET status=?,artifact_name=?,media_type=?,sha256=?,error=?,artifact=?,worker_id=?,attempts=?,started_at=?,lease_until=?,completed_at=? WHERE id=?`), value.Status, value.ArtifactName, value.MediaType, value.SHA256, value.Error, value.Artifact, value.WorkerID, value.Attempts, nullableTimePtrMillis(value.StartedAt), nullableTimePtrMillis(value.LeaseUntil), nullableTimePtrMillis(value.CompletedAt), value.ID)
	return affected(result, err)
}
func (s *Store) ClaimClientBuild(ctx context.Context, workerID string, formats, platforms, architectures []string, now, leaseUntil time.Time) (domain.ClientBuild, error) {
	if len(formats) == 0 || len(platforms) == 0 || len(architectures) == 0 {
		return domain.ClientBuild{}, domain.ErrNotFound
	}
	formatPlaceholders := make([]string, len(formats))
	platformPlaceholders := make([]string, len(platforms))
	architecturePlaceholders := make([]string, len(architectures))
	args := make([]any, 0, len(formats)+len(platforms)+len(architectures)+5)
	for index, value := range formats {
		formatPlaceholders[index] = "?"
		args = append(args, value)
	}
	for index, value := range platforms {
		platformPlaceholders[index] = "?"
		args = append(args, value)
	}
	for index, value := range architectures {
		architecturePlaceholders[index] = "?"
		args = append(args, value)
	}
	query := `UPDATE client_builds SET status='leased',worker_id=?,attempts=attempts+1,started_at=COALESCE(started_at,?),lease_until=?,error='' WHERE id=(SELECT id FROM client_builds WHERE format IN (` + strings.Join(formatPlaceholders, ",") + `) AND target_os IN (` + strings.Join(platformPlaceholders, ",") + `) AND architecture IN (` + strings.Join(architecturePlaceholders, ",") + `) AND attempts<5 AND (status='queued' OR (status IN ('leased','building','uploading') AND lease_until<?)) ORDER BY created_at LIMIT 1) AND attempts<5 AND (status='queued' OR lease_until<?)`
	args = append([]any{workerID, millis(now), millis(leaseUntil)}, args...)
	args = append(args, millis(now), millis(now))
	result, err := s.db.ExecContext(ctx, s.bind(query), args...)
	if err != nil {
		return domain.ClientBuild{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return domain.ClientBuild{}, err
	}
	if count == 0 {
		return domain.ClientBuild{}, domain.ErrNotFound
	}
	return scanClientBuild(s.db.QueryRowContext(ctx, s.bind(`SELECT id,profile_id,target_os,architecture,format,status,artifact_name,media_type,sha256,error,artifact,created_by,worker_id,attempts,created_at,started_at,lease_until,completed_at FROM client_builds WHERE worker_id=? AND status='leased' AND lease_until=? ORDER BY created_at LIMIT 1`), workerID, millis(leaseUntil)))
}
func (s *Store) UpdateClaimedClientBuild(ctx context.Context, value domain.ClientBuild, workerID string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE client_builds SET status=?,artifact_name=?,media_type=?,sha256=?,error=?,artifact=?,attempts=?,started_at=?,lease_until=?,completed_at=? WHERE id=? AND worker_id=? AND status IN ('leased','building','uploading') AND lease_until>?`), value.Status, value.ArtifactName, value.MediaType, value.SHA256, value.Error, value.Artifact, value.Attempts, nullableTimePtrMillis(value.StartedAt), nullableTimePtrMillis(value.LeaseUntil), nullableTimePtrMillis(value.CompletedAt), value.ID, workerID, millis(now))
	return affected(result, err)
}
func scanClientBuild(row scanner) (domain.ClientBuild, error) {
	var value domain.ClientBuild
	var created int64
	var started, leaseUntil, completed sql.NullInt64
	err := row.Scan(&value.ID, &value.ProfileID, &value.TargetOS, &value.Architecture, &value.Format, &value.Status, &value.ArtifactName, &value.MediaType, &value.SHA256, &value.Error, &value.Artifact, &value.CreatedBy, &value.WorkerID, &value.Attempts, &created, &started, &leaseUntil, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ClientBuild{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ClientBuild{}, err
	}
	value.CreatedAt = fromMillis(created)
	if started.Valid {
		at := fromMillis(started.Int64)
		value.StartedAt = &at
	}
	if leaseUntil.Valid {
		at := fromMillis(leaseUntil.Int64)
		value.LeaseUntil = &at
	}
	if completed.Valid {
		at := fromMillis(completed.Int64)
		value.CompletedAt = &at
	}
	return value, nil
}
func (s *Store) UpsertBuilderWorker(ctx context.Context, value domain.BuilderWorker) error {
	formats, err := json.Marshal(value.Formats)
	if err != nil {
		return err
	}
	platforms, err := json.Marshal(value.Platforms)
	if err != nil {
		return err
	}
	architectures, err := json.Marshal(value.Architectures)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO builder_workers(id,name,hostname,version,formats,platforms,architectures,concurrency,status,token_hash,last_seen_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,hostname=excluded.hostname,version=excluded.version,formats=excluded.formats,platforms=excluded.platforms,architectures=excluded.architectures,concurrency=excluded.concurrency,status=excluded.status,token_hash=CASE WHEN excluded.token_hash='' THEN builder_workers.token_hash ELSE excluded.token_hash END,last_seen_at=excluded.last_seen_at,updated_at=excluded.updated_at`), value.ID, value.Name, value.Hostname, value.Version, string(formats), string(platforms), string(architectures), value.Concurrency, value.Status, value.TokenHash, millis(value.LastSeenAt), millis(value.CreatedAt), millis(value.UpdatedAt))
	return err
}
func (s *Store) FindBuilderWorkerByTokenHash(ctx context.Context, tokenHash string) (domain.BuilderWorker, error) {
	var value domain.BuilderWorker
	var lastSeen, createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT id,name,hostname,version,concurrency,status,last_seen_at,created_at,updated_at FROM builder_workers WHERE token_hash=?`), tokenHash).Scan(&value.ID, &value.Name, &value.Hostname, &value.Version, &value.Concurrency, &value.Status, &lastSeen, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return value, domain.ErrNotFound
	}
	value.LastSeenAt, value.CreatedAt, value.UpdatedAt = fromMillis(lastSeen), fromMillis(createdAt), fromMillis(updatedAt)
	return value, err
}
func (s *Store) ListBuilderWorkers(ctx context.Context) ([]domain.BuilderWorker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,hostname,version,formats,platforms,architectures,concurrency,status,last_seen_at,created_at,updated_at FROM builder_workers ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.BuilderWorker, 0)
	for rows.Next() {
		var value domain.BuilderWorker
		var formats, platforms, architectures string
		var lastSeen, created, updated int64
		if err = rows.Scan(&value.ID, &value.Name, &value.Hostname, &value.Version, &formats, &platforms, &architectures, &value.Concurrency, &value.Status, &lastSeen, &created, &updated); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(formats), &value.Formats) != nil || json.Unmarshal([]byte(platforms), &value.Platforms) != nil || json.Unmarshal([]byte(architectures), &value.Architectures) != nil {
			return nil, errors.New("invalid builder worker capabilities")
		}
		value.LastSeenAt, value.CreatedAt, value.UpdatedAt = fromMillis(lastSeen), fromMillis(created), fromMillis(updated)
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s *Store) CreateAddressBookTag(ctx context.Context, v domain.AddressBookTag) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO address_book_tags(id,address_book_id,name,color,created_at,updated_at) VALUES(?,?,?,?,?,?)`), v.ID, v.AddressBookID, v.Name, v.Color, millis(v.CreatedAt), millis(v.UpdatedAt))
	return err
}
func (s *Store) ListAddressBookTags(ctx context.Context, bookID string) ([]domain.AddressBookTag, error) {
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id,address_book_id,name,color,created_at,updated_at FROM address_book_tags WHERE address_book_id=? ORDER BY name`), bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.AddressBookTag, 0)
	for rows.Next() {
		var value domain.AddressBookTag
		var created, updated int64
		if err := rows.Scan(&value.ID, &value.AddressBookID, &value.Name, &value.Color, &created, &updated); err != nil {
			return nil, err
		}
		value.CreatedAt, value.UpdatedAt = fromMillis(created), fromMillis(updated)
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s *Store) UpdateAddressBookTag(ctx context.Context, v domain.AddressBookTag, oldName string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE address_book_tags SET name=?,color=?,updated_at=? WHERE address_book_id=? AND name=?`), v.Name, v.Color, millis(v.UpdatedAt), v.AddressBookID, oldName)
	return affected(result, err)
}
func (s *Store) DeleteAddressBookTag(ctx context.Context, bookID, name string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM address_book_tags WHERE address_book_id=? AND name=?`), bookID, name)
	return affected(result, err)
}
func (s *Store) CreateRelayServer(ctx context.Context, v domain.RelayServer) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO relay_servers (id,name,hostname,port,region,enabled,health,latency_ms,connections,bandwidth,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`), v.ID, v.Name, v.Hostname, v.Port, v.Region, v.Enabled, v.Health, v.LatencyMS, v.Connections, v.Bandwidth, millis(v.CreatedAt), millis(v.UpdatedAt))
	return err
}
func (s *Store) ListRelayServers(ctx context.Context) ([]domain.RelayServer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,hostname,port,region,enabled,health,latency_ms,connections,bandwidth,created_at,updated_at FROM relay_servers ORDER BY enabled DESC,region,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.RelayServer, 0)
	for rows.Next() {
		var v domain.RelayServer
		var created, updated int64
		if err := rows.Scan(&v.ID, &v.Name, &v.Hostname, &v.Port, &v.Region, &v.Enabled, &v.Health, &v.LatencyMS, &v.Connections, &v.Bandwidth, &created, &updated); err != nil {
			return nil, err
		}
		v.CreatedAt, v.UpdatedAt = fromMillis(created), fromMillis(updated)
		values = append(values, v)
	}
	return values, rows.Err()
}
func (s *Store) UpdateRelayServer(ctx context.Context, v domain.RelayServer) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE relay_servers SET name=?,hostname=?,port=?,region=?,enabled=?,health=?,updated_at=? WHERE id=?`), v.Name, v.Hostname, v.Port, v.Region, v.Enabled, v.Health, millis(v.UpdatedAt), v.ID)
	return affected(result, err)
}
func (s *Store) UpdateRelayHealth(ctx context.Context, id, health string, latencyMS int, checkedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE relay_servers SET health=?,latency_ms=?,updated_at=? WHERE id=?`), health, latencyMS, millis(checkedAt), id)
	return affected(result, err)
}
func (s *Store) UpsertRelayTelemetry(ctx context.Context, v domain.RelayServer) (domain.RelayServer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.RelayServer{}, err
	}
	defer tx.Rollback()
	now := millis(v.UpdatedAt)
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE relay_servers SET health='healthy',connections=?,bandwidth=?,updated_at=? WHERE id=?`), v.Connections, v.Bandwidth, now, v.ID)
	if err != nil {
		return domain.RelayServer{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return domain.RelayServer{}, err
	}
	if changed == 0 {
		result, err = tx.ExecContext(ctx, s.bind(`UPDATE relay_servers SET health='healthy',connections=?,bandwidth=?,updated_at=? WHERE hostname=? AND port=?`), v.Connections, v.Bandwidth, now, v.Hostname, v.Port)
		if err != nil {
			return domain.RelayServer{}, err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return domain.RelayServer{}, err
		}
	}
	if changed == 0 {
		_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO relay_servers (id,name,hostname,port,region,enabled,health,latency_ms,connections,bandwidth,created_at,updated_at) VALUES (?,?,?,?,?,TRUE,'healthy',0,?,?,?,?)`), v.ID, v.Name, v.Hostname, v.Port, v.Region, v.Connections, v.Bandwidth, now, now)
		if err != nil {
			return domain.RelayServer{}, err
		}
	}
	row := tx.QueryRowContext(ctx, s.bind(`SELECT id,name,hostname,port,region,enabled,health,latency_ms,connections,bandwidth,created_at,updated_at FROM relay_servers WHERE id=? OR (hostname=? AND port=?) ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END LIMIT 1`), v.ID, v.Hostname, v.Port, v.ID)
	var value domain.RelayServer
	var created, updated int64
	if err := row.Scan(&value.ID, &value.Name, &value.Hostname, &value.Port, &value.Region, &value.Enabled, &value.Health, &value.LatencyMS, &value.Connections, &value.Bandwidth, &created, &updated); err != nil {
		return domain.RelayServer{}, err
	}
	value.CreatedAt, value.UpdatedAt = fromMillis(created), fromMillis(updated)
	if err := tx.Commit(); err != nil {
		return domain.RelayServer{}, err
	}
	return value, nil
}
func (s *Store) DeleteRelayServer(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM relay_servers WHERE id=?`), id)
	return affected(result, err)
}
func (s *Store) AppendRelayMetric(ctx context.Context, value domain.RelayMetric) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO relay_metrics(relay_id,recorded_at,health,latency_ms,connections,bandwidth) VALUES(?,?,?,?,?,?) ON CONFLICT(relay_id,recorded_at) DO UPDATE SET health=excluded.health,latency_ms=excluded.latency_ms,connections=excluded.connections,bandwidth=excluded.bandwidth`), value.RelayID, millis(value.RecordedAt), value.Health, value.LatencyMS, value.Connections, value.Bandwidth)
	return err
}
func (s *Store) ListRelayMetrics(ctx context.Context, relayID string, since time.Time, limit int) ([]domain.RelayMetric, error) {
	if limit < 1 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT relay_id,recorded_at,health,latency_ms,connections,bandwidth FROM relay_metrics WHERE relay_id=? AND recorded_at>=? ORDER BY recorded_at DESC LIMIT ?`), relayID, millis(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.RelayMetric, 0)
	for rows.Next() {
		var value domain.RelayMetric
		var recorded int64
		if err = rows.Scan(&value.RelayID, &recorded, &value.Health, &value.LatencyMS, &value.Connections, &value.Bandwidth); err != nil {
			return nil, err
		}
		value.RecordedAt = fromMillis(recorded)
		values = append(values, value)
	}
	return values, rows.Err()
}
func (s *Store) PruneRelayMetrics(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM relay_metrics WHERE recorded_at<?`), millis(before))
	return err
}
func (s *Store) CreateACLRule(ctx context.Context, value domain.ACLRule) error {
	p, _ := json.Marshal(value.Permissions)
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO acl_rules (id,name,subject_type,subject_id,target_type,target_id,permissions,effect,enabled,priority,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`), value.ID, value.Name, value.SubjectType, value.SubjectID, value.TargetType, value.TargetID, string(p), value.Effect, value.Enabled, value.Priority, millis(value.CreatedAt), millis(value.UpdatedAt))
	return err
}
func (s *Store) ListACLRules(ctx context.Context) ([]domain.ACLRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,subject_type,subject_id,target_type,target_id,permissions,effect,enabled,priority,created_at,updated_at FROM acl_rules ORDER BY priority,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ACLRule{}
	for rows.Next() {
		var v domain.ACLRule
		var p string
		var c, u int64
		if err := rows.Scan(&v.ID, &v.Name, &v.SubjectType, &v.SubjectID, &v.TargetType, &v.TargetID, &p, &v.Effect, &v.Enabled, &v.Priority, &c, &u); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(p), &v.Permissions)
		v.CreatedAt, v.UpdatedAt = fromMillis(c), fromMillis(u)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateACLRule(ctx context.Context, value domain.ACLRule) error {
	permissions, _ := json.Marshal(value.Permissions)
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE acl_rules SET name=?,subject_type=?,subject_id=?,target_type=?,target_id=?,permissions=?,effect=?,enabled=?,priority=?,updated_at=? WHERE id=?`), value.Name, value.SubjectType, value.SubjectID, value.TargetType, value.TargetID, string(permissions), value.Effect, value.Enabled, value.Priority, millis(value.UpdatedAt), value.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return domain.ErrNotFound
	}
	return err
}
func (s *Store) DeleteACLRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM acl_rules WHERE id=?`), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return domain.ErrNotFound
	}
	return err
}
func (s *Store) CreateStrategy(ctx context.Context, value domain.Strategy) error {
	settings, _ := json.Marshal(value.Settings)
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO strategies (id,name,scope_type,scope_id,priority,settings,enabled,created_at,updated_at,deleted) VALUES (?,?,?,?,?,?,?,?,?,FALSE)`), value.ID, value.Name, value.ScopeType, value.ScopeID, value.Priority, string(settings), value.Enabled, millis(value.CreatedAt), millis(value.UpdatedAt))
	return err
}
func (s *Store) ListStrategies(ctx context.Context) ([]domain.Strategy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,scope_type,scope_id,priority,settings,enabled,created_at,updated_at,deleted FROM strategies ORDER BY priority,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Strategy{}
	for rows.Next() {
		var v domain.Strategy
		var settings string
		var c, u int64
		if err := rows.Scan(&v.ID, &v.Name, &v.ScopeType, &v.ScopeID, &v.Priority, &settings, &v.Enabled, &c, &u, &v.Deleted); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(settings), &v.Settings)
		v.CreatedAt, v.UpdatedAt = fromMillis(c), fromMillis(u)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateStrategy(ctx context.Context, value domain.Strategy) error {
	settings, _ := json.Marshal(value.Settings)
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE strategies SET name=?,scope_type=?,scope_id=?,priority=?,settings=?,enabled=?,updated_at=?,deleted=FALSE WHERE id=?`), value.Name, value.ScopeType, value.ScopeID, value.Priority, string(settings), value.Enabled, millis(value.UpdatedAt), value.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return domain.ErrNotFound
	}
	return err
}
func (s *Store) DeleteStrategy(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE strategies SET enabled=FALSE,deleted=TRUE,updated_at=? WHERE id=? AND deleted=FALSE`), millis(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return domain.ErrNotFound
	}
	return err
}

const userColumns = `id, username, email, phone, password_hash, display_name, role, enabled, approval_status, token_version, created_at, updated_at, last_login_at, force_relogin_at, totp_secret, totp_enabled`

func (s *Store) CreateRole(ctx context.Context, role domain.RoleDefinition) error {
	permissions, err := json.Marshal(role.Permissions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.bind(`INSERT INTO roles(id,name,description,permissions,system,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`), role.ID, role.Name, role.Description, string(permissions), role.System, millis(role.CreatedAt), millis(role.UpdatedAt))
	return err
}

func (s *Store) ListRoles(ctx context.Context) ([]domain.RoleDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,permissions,system,created_at,updated_at FROM roles ORDER BY system DESC,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := make([]domain.RoleDefinition, 0)
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s *Store) FindRoleByID(ctx context.Context, id domain.Role) (domain.RoleDefinition, error) {
	return scanRole(s.db.QueryRowContext(ctx, s.bind(`SELECT id,name,description,permissions,system,created_at,updated_at FROM roles WHERE id=?`), id))
}

func (s *Store) UpdateRole(ctx context.Context, role domain.RoleDefinition) error {
	permissions, err := json.Marshal(role.Permissions)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, s.bind(`UPDATE roles SET name=?,description=?,permissions=?,updated_at=? WHERE id=? AND system=FALSE`), role.Name, role.Description, string(permissions), millis(role.UpdatedAt), role.ID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRole(ctx context.Context, id domain.Role) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var system bool
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT system FROM roles WHERE id=?`), id).Scan(&system); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	} else if err != nil {
		return err
	}
	if system {
		return errors.New("system role cannot be deleted")
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE users SET role=?,token_version=token_version+1 WHERE role=?`), domain.RoleUser, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM roles WHERE id=?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

func scanRole(row rowScanner) (domain.RoleDefinition, error) {
	var role domain.RoleDefinition
	var permissions string
	var created, updated int64
	if err := row.Scan(&role.ID, &role.Name, &role.Description, &permissions, &role.System, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return role, domain.ErrNotFound
		}
		return role, err
	}
	if err := json.Unmarshal([]byte(permissions), &role.Permissions); err != nil {
		return role, err
	}
	role.CreatedAt, role.UpdatedAt = fromMillis(created), fromMillis(updated)
	return role, nil
}

const sessionColumns = `id, user_id, created_at, expires_at, revoked_at, last_seen_at, ip, user_agent, client_device_id`

type scanner interface{ Scan(...any) error }

func (s *Store) scanUser(row scanner) (domain.User, error) {
	var user domain.User
	var created, updated int64
	var lastLogin, forceRelogin sql.NullInt64
	err := row.Scan(&user.ID, &user.Username, &user.Email, &user.Phone, &user.PasswordHash, &user.DisplayName,
		&user.Role, &user.Enabled, &user.ApprovalStatus, &user.TokenVersion, &created, &updated, &lastLogin, &forceRelogin, &user.TOTPSecret, &user.TOTPEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	user.CreatedAt, user.UpdatedAt = fromMillis(created), fromMillis(updated)
	if lastLogin.Valid {
		user.LastLoginAt = fromMillis(lastLogin.Int64)
	}
	if forceRelogin.Valid {
		user.ForceReloginAt = fromMillis(forceRelogin.Int64)
	}
	return user, nil
}

func scanSession(row scanner) (domain.Session, error) {
	var session domain.Session
	var created, expires, lastSeen int64
	var revoked sql.NullInt64
	err := row.Scan(&session.ID, &session.UserID, &created, &expires, &revoked, &lastSeen,
		&session.IP, &session.UserAgent, &session.ClientDeviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Session{}, err
	}
	session.CreatedAt, session.ExpiresAt, session.LastSeenAt = fromMillis(created), fromMillis(expires), fromMillis(lastSeen)
	if revoked.Valid {
		value := fromMillis(revoked.Int64)
		session.RevokedAt = &value
	}
	return session, nil
}

func (s *Store) bind(query string) string {
	if s.dialect != "postgres" {
		return query
	}
	var builder strings.Builder
	index := 1
	for _, character := range query {
		if character == '?' {
			fmt.Fprintf(&builder, "$%d", index)
			index++
		} else {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func millis(value time.Time) int64     { return value.UTC().UnixMilli() }
func fromMillis(value int64) time.Time { return time.UnixMilli(value).UTC() }
func nullableMillis(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return millis(value)
}

func affected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return domain.ErrNotFound
	}
	return nil
}

var _ domain.Repository = (*Store)(nil)

func (s *Store) GetUserPreference(ctx context.Context, userID, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT value FROM user_preferences WHERE user_id=? AND key=?`), userID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return value, err
}

func (s *Store) UpsertUserPreference(ctx context.Context, userID, key, value string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`INSERT INTO user_preferences(user_id,key,value,updated_at) VALUES(?,?,?,?) ON CONFLICT(user_id,key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`), userID, key, value, millis(now))
	return err
}
