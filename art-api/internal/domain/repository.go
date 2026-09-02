package domain

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrLastAdmin = errors.New("cannot delete last administrator")
var ErrUnsupported = errors.New("unsupported")

type Repository interface {
	Close() error
	Migrate(context.Context) error
	CountUsers(context.Context) (int64, error)
	CreateUser(context.Context, User) error
	CreateRegisteredUser(context.Context, User) error
	ListUsers(context.Context) ([]User, error)
	FindUserByUsername(context.Context, string) (User, error)
	FindUserByID(context.Context, string) (User, error)
	SetUserEnabled(context.Context, string, bool, time.Time) (User, error)
	SetUserApproval(context.Context, string, ApprovalStatus, time.Time) (User, error)
	ForceRelogin(context.Context, string, time.Time) (User, error)
	UpdateUserPassword(context.Context, string, string, time.Time) (User, error)
	UpdateUser(context.Context, string, UserUpdate, time.Time) (User, error)
	DeleteUser(context.Context, string) error
	SetUserTOTP(context.Context, string, string, bool, time.Time) (User, error)
	ReplaceMFARecoveryCodes(context.Context, string, []string, time.Time) error
	ConsumeMFARecoveryCode(context.Context, string, string) (bool, error)
	CountMFARecoveryCodes(context.Context, string) (int, error)
	CreateAuthChallenge(context.Context, AuthChallenge) error
	FindAuthChallenge(context.Context, string, time.Time) (AuthChallenge, error)
	ConsumeAuthChallenge(context.Context, string, time.Time) (AuthChallenge, error)
	CreateOIDCAuthRequest(context.Context, OIDCAuthRequest) error
	FindOIDCAuthRequestByState(context.Context, string, time.Time) (OIDCAuthRequest, error)
	CompleteOIDCAuthRequest(context.Context, string, string, string) error
	ConsumeOIDCAuthRequest(context.Context, string, string, string, time.Time) (OIDCAuthRequest, error)
	ConsumeOIDCLinkRequest(context.Context, string, string, time.Time) (OIDCAuthRequest, error)
	FindOIDCIdentity(context.Context, string, string) (OIDCIdentity, error)
	CreateOIDCIdentity(context.Context, OIDCIdentity) error
	ListOIDCIdentities(context.Context, string) ([]OIDCIdentity, error)
	DeleteOIDCIdentity(context.Context, string, string, string) error
	UpdateLastLogin(context.Context, string, time.Time) error
	CreateSession(context.Context, Session) error
	FindSession(context.Context, string) (Session, error)
	TouchSession(context.Context, string, time.Time) error
	RevokeSession(context.Context, string, time.Time) (Session, error)
	RevokeUserSessions(context.Context, string, time.Time) error
	ListAuthState(context.Context, time.Time) ([]User, []Session, error)
	AppendAudit(context.Context, AuditEvent) error
	ListAudit(context.Context, int) ([]AuditEvent, error)
	ListSessions(context.Context, time.Time) ([]Session, error)
	UpsertDevice(context.Context, Device) error
	ListDevices(context.Context) ([]Device, error)
	UpdateDeviceManagement(context.Context, string, string, string, []string) (Device, error)
	BulkUpdateDevices(context.Context, []string, *string, []string, []string) error
	SetDeviceArchived(context.Context, string, bool, time.Time) (Device, error)
	DeleteArchivedDevice(context.Context, string) error
	ImportDeviceManagement(context.Context, []DeviceManagementImport) error
	CreateAPIToken(context.Context, APIToken) error
	ListAPITokens(context.Context, string) ([]APIToken, error)
	FindAPITokenByHash(context.Context, string) (APIToken, error)
	TouchAPIToken(context.Context, string, time.Time) error
	RevokeAPIToken(context.Context, string, string, time.Time) error
	Backup(context.Context, string) error
	InspectBackup(context.Context, string) (BackupInspection, error)
	CreateGroup(context.Context, Group) error
	ListGroups(context.Context) ([]Group, error)
	FindGroupByID(context.Context, string) (Group, error)
	UpdateGroup(context.Context, Group) error
	DeleteGroup(context.Context, string) error
	SetUserGroupMember(context.Context, string, string, bool) error
	ListUserGroupMemberships(context.Context) ([]UserGroupMembership, error)
	CreateAddressBook(context.Context, AddressBook) error
	ListAddressBooks(context.Context) ([]AddressBook, error)
	FindAddressBookByID(context.Context, string) (AddressBook, error)
	UpdateAddressBook(context.Context, AddressBook) error
	DeleteAddressBook(context.Context, string) error
	UpsertAddressBookGrant(context.Context, AddressBookGrant) error
	ListAddressBookGrants(context.Context, string) ([]AddressBookGrant, error)
	ListAllAddressBookGrants(context.Context) ([]AddressBookGrant, error)
	DeleteAddressBookGrant(context.Context, string, string) error
	CreateAddressBookEntry(context.Context, AddressBookEntry) error
	ListAddressBookEntries(context.Context, string) ([]AddressBookEntry, error)
	ReplaceAddressBookEntries(context.Context, string, []AddressBookEntry) error
	UpdateAddressBookEntry(context.Context, AddressBookEntry) error
	DeleteAddressBookEntry(context.Context, string, string) error
	CreateAddressBookTag(context.Context, AddressBookTag) error
	ListAddressBookTags(context.Context, string) ([]AddressBookTag, error)
	UpdateAddressBookTag(context.Context, AddressBookTag, string) error
	DeleteAddressBookTag(context.Context, string, string) error
	CreateRelayServer(context.Context, RelayServer) error
	ListRelayServers(context.Context) ([]RelayServer, error)
	UpdateRelayServer(context.Context, RelayServer) error
	UpdateRelayHealth(context.Context, string, string, int, time.Time) error
	UpsertRelayTelemetry(context.Context, RelayServer) (RelayServer, error)
	DeleteRelayServer(context.Context, string) error
	AppendRelayMetric(context.Context, RelayMetric) error
	ListRelayMetrics(context.Context, string, time.Time, int) ([]RelayMetric, error)
	PruneRelayMetrics(context.Context, time.Time) error
	CreateWebhook(context.Context, Webhook) error
	ListWebhooks(context.Context) ([]Webhook, error)
	FindWebhookByID(context.Context, string) (Webhook, error)
	UpdateWebhook(context.Context, Webhook) error
	DeleteWebhook(context.Context, string) error
	CreateWebhookDelivery(context.Context, WebhookDelivery) error
	ListWebhookDeliveries(context.Context, string, int) ([]WebhookDelivery, error)
	ListDueWebhookDeliveries(context.Context, time.Time, int) ([]WebhookDelivery, error)
	UpdateWebhookDelivery(context.Context, WebhookDelivery) error
	CreateNotification(context.Context, Notification) error
	ListNotifications(context.Context, int, bool) ([]Notification, error)
	MarkNotificationRead(context.Context, string, time.Time) error
	MarkAllNotificationsRead(context.Context, time.Time) error
	CreateClientProfile(context.Context, ClientProfile) error
	ListClientProfiles(context.Context) ([]ClientProfile, error)
	FindClientProfileByID(context.Context, string) (ClientProfile, error)
	UpdateClientProfile(context.Context, ClientProfile) error
	DeleteClientProfile(context.Context, string) error
	CreateClientProfileAssignment(context.Context, ClientProfileAssignment) error
	ListClientProfileAssignments(context.Context) ([]ClientProfileAssignment, error)
	DeleteClientProfileAssignment(context.Context, string) error
	CreateClientBuild(context.Context, ClientBuild) error
	ListClientBuilds(context.Context, int) ([]ClientBuild, error)
	FindClientBuildByID(context.Context, string) (ClientBuild, error)
	UpdateClientBuild(context.Context, ClientBuild) error
	ClaimClientBuild(context.Context, string, []string, []string, []string, time.Time, time.Time) (ClientBuild, error)
	UpdateClaimedClientBuild(context.Context, ClientBuild, string, time.Time) error
	UpsertBuilderWorker(context.Context, BuilderWorker) error
	ListBuilderWorkers(context.Context) ([]BuilderWorker, error)
	CreateACLRule(context.Context, ACLRule) error
	ListACLRules(context.Context) ([]ACLRule, error)
	UpdateACLRule(context.Context, ACLRule) error
	DeleteACLRule(context.Context, string) error
	CreateStrategy(context.Context, Strategy) error
	ListStrategies(context.Context) ([]Strategy, error)
	UpdateStrategy(context.Context, Strategy) error
	DeleteStrategy(context.Context, string) error
	CreateRole(context.Context, RoleDefinition) error
	ListRoles(context.Context) ([]RoleDefinition, error)
	FindRoleByID(context.Context, Role) (RoleDefinition, error)
	UpdateRole(context.Context, RoleDefinition) error
	DeleteRole(context.Context, Role) error
	ListRuntimeSettings(context.Context) (map[string]string, error)
	UpsertRuntimeSettings(context.Context, map[string]string, time.Time) error
	GetUserPreference(context.Context, string, string) (string, error)
	UpsertUserPreference(context.Context, string, string, string, time.Time) error
}
