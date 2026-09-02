package domain

import "time"

type Role string
type ApprovalStatus string

type RoleDefinition struct {
	ID          Role      `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	System      bool      `json:"system"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const (
	PermissionAll                 = "*"
	PermissionAdminPortal         = "admin.portal"
	PermissionUsersRead           = "users.read"
	PermissionUsersWrite          = "users.write"
	PermissionDevicesRead         = "devices.read"
	PermissionDevicesWrite        = "devices.write"
	PermissionGroupsRead          = "groups.read"
	PermissionGroupsWrite         = "groups.write"
	PermissionAddressBooksRead    = "address_books.read"
	PermissionAddressBooksWrite   = "address_books.write"
	PermissionACLRead             = "acl.read"
	PermissionACLWrite            = "acl.write"
	PermissionStrategiesRead      = "strategies.read"
	PermissionStrategiesWrite     = "strategies.write"
	PermissionSessionsRead        = "sessions.read"
	PermissionSessionsRevoke      = "sessions.revoke"
	PermissionAuditRead           = "audit.read"
	PermissionRelaysRead          = "relays.read"
	PermissionRelaysWrite         = "relays.write"
	PermissionInfrastructureRead  = "infrastructure.read"
	PermissionInfrastructureWrite = "infrastructure.write"
	PermissionSettingsRead        = "settings.read"
	PermissionSettingsWrite       = "settings.write"
	PermissionBackupRead          = "backup.read"
	PermissionBackupWrite         = "backup.write"
	PermissionRolesRead           = "roles.read"
	PermissionRolesWrite          = "roles.write"
	PermissionWebhooksRead        = "webhooks.read"
	PermissionWebhooksWrite       = "webhooks.write"
	PermissionClientProfilesRead  = "client_profiles.read"
	PermissionClientProfilesWrite = "client_profiles.write"
	PermissionAutomationRead      = "automation.read"
	PermissionAutomationWrite     = "automation.write"
)

var AvailablePermissions = []string{
	PermissionAdminPortal, PermissionUsersRead, PermissionUsersWrite, PermissionDevicesRead,
	PermissionDevicesWrite, PermissionGroupsRead, PermissionGroupsWrite, PermissionAddressBooksRead,
	PermissionAddressBooksWrite, PermissionACLRead, PermissionACLWrite, PermissionStrategiesRead,
	PermissionStrategiesWrite, PermissionSessionsRead, PermissionSessionsRevoke, PermissionAuditRead,
	PermissionRelaysRead, PermissionRelaysWrite, PermissionInfrastructureRead, PermissionInfrastructureWrite, PermissionSettingsRead, PermissionSettingsWrite,
	PermissionRolesRead, PermissionRolesWrite, PermissionBackupRead, PermissionBackupWrite, PermissionWebhooksRead, PermissionWebhooksWrite,
	PermissionClientProfilesRead, PermissionClientProfilesWrite,
	PermissionAutomationRead, PermissionAutomationWrite,
}

const (
	RoleAdmin            Role           = "admin"
	RoleUser             Role           = "user"
	ApprovalPending      ApprovalStatus = "pending"
	ApprovalApproved     ApprovalStatus = "approved"
	ApprovalRejected     ApprovalStatus = "rejected"
	PendingUsersGroupID                 = "system-pending-users"
	ApprovedUsersGroupID                = "system-approved-users"
)

type User struct {
	ID             string         `json:"id"`
	Username       string         `json:"username"`
	Email          string         `json:"email"`
	Phone          string         `json:"phone"`
	PasswordHash   string         `json:"-"`
	DisplayName    string         `json:"display_name"`
	Role           Role           `json:"role"`
	Permissions    []string       `json:"permissions,omitempty"`
	Enabled        bool           `json:"enabled"`
	ApprovalStatus ApprovalStatus `json:"approval_status"`
	TokenVersion   int64          `json:"token_version"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	LastLoginAt    time.Time      `json:"last_login_at,omitempty"`
	ForceReloginAt time.Time      `json:"force_relogin_at,omitempty"`
	TOTPSecret     string         `json:"-"`
	TOTPEnabled    bool           `json:"totp_enabled"`
}

type UserUpdate struct {
	Username, Email, Phone, DisplayName, PasswordHash string
	Role                                              Role
	Enabled                                           bool
	GroupIDs                                          []string
}

type Session struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	IP             string     `json:"ip"`
	UserAgent      string     `json:"user_agent"`
	ClientDeviceID string     `json:"client_device_id"`
}

type SessionRecord struct {
	Session
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	UserEnabled bool   `json:"user_enabled"`
	Status      string `json:"status"`
	Current     bool   `json:"current"`
}

type SessionQuery struct {
	Limit  int
	Offset int
	Status string
	UserID string
	Search string
	Now    time.Time
}

type SessionPage struct {
	Sessions []SessionRecord `json:"sessions"`
	Total    int64           `json:"total"`
	Limit    int             `json:"limit"`
	Offset   int             `json:"offset"`
}

type SessionSummary struct {
	Total   int64 `json:"total"`
	Active  int64 `json:"active"`
	Revoked int64 `json:"revoked"`
	Expired int64 `json:"expired"`
}

type AuthChallenge struct {
	ID             string
	UserID         string
	TokenVersion   int64
	CreatedAt      time.Time
	ExpiresAt      time.Time
	IP             string
	UserAgent      string
	ClientDeviceID string
	RustDeskID     string
	ClientUUID     string
	Platform       string
	ClientType     string
	DeviceName     string
}

type OIDCAuthRequest struct {
	State, PollCode, Provider, Verifier, Nonce                              string
	UserID, LinkUserID, Error                                               string
	RustDeskID, ClientUUID, Platform, ClientType, DeviceName, IP, UserAgent string
	CreatedAt, ExpiresAt                                                    time.Time
}

type OIDCIdentity struct {
	Provider  string    `json:"provider"`
	Subject   string    `json:"subject"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

func (s Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

type AuditEvent struct {
	ID               string         `json:"id"`
	OccurredAt       time.Time      `json:"occurred_at"`
	Type             string         `json:"type"`
	ActorUserID      string         `json:"actor_user_id,omitempty"`
	ActorSessionID   string         `json:"actor_session_id,omitempty"`
	ControllerDevice string         `json:"controller_device_id,omitempty"`
	TargetRustDeskID string         `json:"target_rustdesk_id,omitempty"`
	IP               string         `json:"ip,omitempty"`
	Result           string         `json:"result,omitempty"`
	Reason           string         `json:"reason,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type AuditQuery struct {
	Limit       int
	Offset      int
	Type        string
	Result      string
	ActorUserID string
	TargetID    string
	IP          string
	Search      string
	From        *time.Time
	To          *time.Time
}

type AuditPage struct {
	Events []AuditEvent `json:"events"`
	Total  int64        `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

type AuditSummary struct {
	Total              int64            `json:"total"`
	AllowedConnections int64            `json:"allowed_connections"`
	DeniedConnections  int64            `json:"denied_connections"`
	FailedLogins       int64            `json:"failed_logins"`
	ByType             map[string]int64 `json:"by_type"`
	ByResult           map[string]int64 `json:"by_result"`
}

type ConnectionRecord struct {
	Key              string     `json:"key"`
	ActorUserID      string     `json:"actor_user_id,omitempty"`
	ActorSessionID   string     `json:"actor_session_id,omitempty"`
	ControllerDevice string     `json:"controller_device_id,omitempty"`
	ControllerName   string     `json:"controller_name,omitempty"`
	ControllerLogin  string     `json:"controller_login,omitempty"`
	TargetRustDeskID string     `json:"target_rustdesk_id"`
	ConnectionType   int        `json:"connection_type"`
	IP               string     `json:"ip,omitempty"`
	Transport        string     `json:"transport,omitempty"`
	RelayUUID        string     `json:"relay_uuid,omitempty"`
	RelayServer      string     `json:"relay_server,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	LastSeenAt       time.Time  `json:"last_seen_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
}

type AuthSnapshot struct {
	RequireLogin            bool                  `json:"require_login"`
	RequireDeviceDeployment bool                  `json:"require_device_deployment"`
	SourceID                string                `json:"source_id"`
	Revision                int64                 `json:"revision"`
	CreatedAt               time.Time             `json:"created_at"`
	Users                   []User                `json:"users"`
	Sessions                []Session             `json:"sessions"`
	Devices                 []Device              `json:"devices"`
	ACLRules                []ACLRule             `json:"acl_rules"`
	Strategies              []Strategy            `json:"strategies"`
	UserGroupMemberships    []UserGroupMembership `json:"user_group_memberships"`
	RelayServers            []RelayServer         `json:"relay_servers"`
}

type Device struct {
	RustDeskID  string     `json:"rustdesk_id"`
	ClientUUID  string     `json:"client_uuid"`
	Hostname    string     `json:"hostname"`
	Alias       string     `json:"alias"`
	Platform    string     `json:"platform"`
	Version     string     `json:"version"`
	CPU         string     `json:"cpu"`
	Memory      string     `json:"memory"`
	Username    string     `json:"username"`
	LastSeenIP  string     `json:"last_seen_ip"`
	Online      bool       `json:"online"`
	LastSeen    time.Time  `json:"last_seen"`
	OwnerUserID string     `json:"owner_user_id"`
	GroupID     string     `json:"group_id"`
	Tags        []string   `json:"tags"`
	PublicKey   string     `json:"public_key,omitempty"`
	Deployed    bool       `json:"deployed"`
	DeployedBy  string     `json:"deployed_by,omitempty"`
	DeployedAt  time.Time  `json:"deployed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
}

type BackupInspection struct {
	Valid        bool      `json:"valid"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at"`
	QuickCheck   string    `json:"quick_check"`
	SchemaTables int       `json:"schema_tables"`
	Users        int64     `json:"users"`
	Devices      int64     `json:"devices"`
	Sessions     int64     `json:"sessions"`
}

type BackupArtifact struct {
	Name       string    `json:"name"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	Users      int64     `json:"users"`
	Devices    int64     `json:"devices"`
	Sessions   int64     `json:"sessions"`
	QuickCheck string    `json:"quick_check"`
}

type DeviceManagementImport struct {
	RustDeskID string
	Alias      string
	GroupID    string
	Tags       []string
}

type APIToken struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type GroupKind string

const (
	GroupKindUser   GroupKind = "user"
	GroupKindDevice GroupKind = "device"
)

type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Kind        GroupKind `json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UserGroupMembership struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
	Active  bool   `json:"active"`
}

type AddressBook struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	OwnerUserID string    `json:"owner_user_id"`
	Permission  string    `json:"permission,omitempty"`
	CanManage   bool      `json:"can_manage"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AddressBookGrant struct {
	ID            string    `json:"id"`
	AddressBookID string    `json:"address_book_id"`
	SubjectType   string    `json:"subject_type"`
	SubjectID     string    `json:"subject_id"`
	Permission    string    `json:"permission"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type AddressBookEntry struct {
	ID            string    `json:"id"`
	AddressBookID string    `json:"address_book_id"`
	RustDeskID    string    `json:"rustdesk_id"`
	Alias         string    `json:"alias"`
	Username      string    `json:"username"`
	Hostname      string    `json:"hostname"`
	Platform      string    `json:"platform"`
	Folder        string    `json:"folder"`
	Favourite     bool      `json:"favourite"`
	Tags          []string  `json:"tags"`
	ForceRelay    bool      `json:"force_always_relay"`
	RDPPort       string    `json:"rdp_port"`
	RDPUsername   string    `json:"rdp_username"`
	LoginName     string    `json:"login_name"`
	SameServer    bool      `json:"same_server"`
	CreatedAt     time.Time `json:"created_at"`
}

type AddressBookTag struct {
	ID            string    `json:"id"`
	AddressBookID string    `json:"address_book_id"`
	Name          string    `json:"name"`
	Color         int64     `json:"color"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type RelayServer struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname"`
	Port        int       `json:"port"`
	Region      string    `json:"region"`
	Enabled     bool      `json:"enabled"`
	Health      string    `json:"health"`
	LatencyMS   int       `json:"latency_ms"`
	Connections int       `json:"connections"`
	Bandwidth   int64     `json:"bandwidth"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Deleted     bool      `json:"deleted,omitempty"`
}

type RelayMetric struct {
	RelayID     string    `json:"relay_id"`
	RecordedAt  time.Time `json:"recorded_at"`
	Health      string    `json:"health"`
	LatencyMS   int       `json:"latency_ms"`
	Connections int       `json:"connections"`
	Bandwidth   int64     `json:"bandwidth"`
}

type Webhook struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type WebhookDelivery struct {
	ID           string     `json:"id"`
	WebhookID    string     `json:"webhook_id"`
	EventType    string     `json:"event_type"`
	Payload      string     `json:"-"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	ResponseCode int        `json:"response_code"`
	LastError    string     `json:"last_error"`
	NextAttempt  time.Time  `json:"next_attempt"`
	CreatedAt    time.Time  `json:"created_at"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
}

// Notification is a durable administrator-facing signal derived from a
// security or infrastructure event. Audit remains the immutable evidence log;
// notifications add triage state without mutating that evidence.
type Notification struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Severity  string     `json:"severity"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	Resource  string     `json:"resource,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
}

// AutomationRule reacts to internal domain events. Conditions and actions are
// intentionally data-driven so adding mail/chat workers later does not change
// the rule persistence contract.
type AutomationRule struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	EventTypes      []string          `json:"event_types"`
	Conditions      map[string]string `json:"conditions"`
	Actions         []string          `json:"actions"`
	Severity        string            `json:"severity"`
	ThrottleSeconds int               `json:"throttle_seconds"`
	Enabled         bool              `json:"enabled"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type AutomationRun struct {
	ID        string    `json:"id"`
	RuleID    string    `json:"rule_id"`
	EventType string    `json:"event_type"`
	EventID   string    `json:"event_id"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type ClusterNode struct {
	ID         string    `json:"id"`
	Service    string    `json:"service"`
	Version    string    `json:"version"`
	Address    string    `json:"address"`
	StartedAt  time.Time `json:"started_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	LeaseCount int       `json:"lease_count"`
}

type ClusterLease struct {
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`
	ExpiresAt time.Time `json:"expires_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ClientProfile struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Platform    string         `json:"platform"`
	Settings    map[string]any `json:"settings"`
	Branding    map[string]any `json:"branding"`
	Version     int64          `json:"version"`
	Enabled     bool           `json:"enabled"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type ClientProfileAssignment struct {
	ID        string    `json:"id"`
	ProfileID string    `json:"profile_id"`
	ScopeType string    `json:"scope_type"`
	ScopeID   string    `json:"scope_id"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
}

type ClientBuild struct {
	ID           string     `json:"id"`
	ProfileID    string     `json:"profile_id"`
	TargetOS     string     `json:"target_os"`
	Architecture string     `json:"architecture"`
	Format       string     `json:"format"`
	Status       string     `json:"status"`
	ArtifactName string     `json:"artifact_name"`
	MediaType    string     `json:"media_type,omitempty"`
	SHA256       string     `json:"sha256"`
	Error        string     `json:"error"`
	Artifact     string     `json:"-"`
	CreatedBy    string     `json:"created_by"`
	WorkerID     string     `json:"worker_id,omitempty"`
	Attempts     int        `json:"attempts"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	LeaseUntil   *time.Time `json:"lease_until,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type BuilderWorker struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Hostname      string    `json:"hostname"`
	Version       string    `json:"version"`
	Formats       []string  `json:"formats"`
	Platforms     []string  `json:"platforms"`
	Architectures []string  `json:"architectures"`
	Concurrency   int       `json:"concurrency"`
	Status        string    `json:"status"`
	LastSeenAt    time.Time `json:"last_seen_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	TokenHash     string    `json:"-"`
}

type ACLRule struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	Permissions []string  `json:"permissions"`
	Effect      string    `json:"effect"`
	Enabled     bool      `json:"enabled"`
	Priority    int       `json:"priority"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Deleted     bool      `json:"deleted,omitempty"`
}

type Strategy struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	ScopeType string         `json:"scope_type"`
	ScopeID   string         `json:"scope_id"`
	Priority  int            `json:"priority"`
	Settings  map[string]any `json:"settings"`
	Enabled   bool           `json:"enabled"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Deleted   bool           `json:"deleted,omitempty"`
}
