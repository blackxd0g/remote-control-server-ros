package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	addressbookservice "github.com/art-rustdesk/platform/art-api/internal/addressbook"
	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/auth"
	backupservice "github.com/art-rustdesk/platform/art-api/internal/backup"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/managedclient"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
	"github.com/art-rustdesk/platform/art-api/internal/oidcauth"
	"github.com/art-rustdesk/platform/art-api/internal/presence"
	relayservice "github.com/art-rustdesk/platform/art-api/internal/relay"
	"github.com/art-rustdesk/platform/art-api/internal/runtimeconfig"
	strategyservice "github.com/art-rustdesk/platform/art-api/internal/strategy"
	webhookservice "github.com/art-rustdesk/platform/art-api/internal/webhook"
	"github.com/art-rustdesk/platform/art-api/internal/webui"
	"github.com/google/uuid"
)

type Server struct {
	mux                 *http.ServeMux
	auth                *auth.Service
	mfa                 *mfa.Service
	oidc                *oidcauth.Service
	ldapEnabled         bool
	ldapAutoProvision   bool
	audit               *audit.Service
	repository          domain.Repository
	addressBooks        *addressbookservice.Service
	hub                 *events.Hub
	internalSecret      []byte
	builderToken        []byte
	loginLimiter        *LoginLimiter
	registrationLimiter *LoginLimiter
	registrationEnabled bool
	runtime             *runtimeState
	strategies          *strategyservice.Service
	webhooks            *webhookservice.Service
	managedClients      *managedclient.Service
	brandingDir         string
	backups             *backupservice.Service
	configuration       *runtimeconfig.Service
	metricsToken        []byte
	trustedProxies      []netip.Prefix
}

func New(authService *auth.Service, mfaService *mfa.Service, auditService *audit.Service, repository domain.Repository,
	hub *events.Hub, internalSecret []byte, loginLimiter *LoginLimiter) *Server {
	server := &Server{mux: http.NewServeMux(), auth: authService, mfa: mfaService, audit: auditService,
		repository: repository, hub: hub, internalSecret: internalSecret, loginLimiter: loginLimiter,
		addressBooks: addressbookservice.New(repository), strategies: strategyservice.New(repository), runtime: newRuntimeState()}
	server.routes()
	return server
}

func (s *Server) EnableOIDC(service *oidcauth.Service) *Server { s.oidc = service; return s }
func (s *Server) EnableLDAP(enabled, autoProvision bool) *Server {
	s.ldapEnabled, s.ldapAutoProvision = enabled, autoProvision
	return s
}
func (s *Server) EnableWebhooks(service *webhookservice.Service) *Server {
	s.webhooks = service
	return s
}
func (s *Server) EnableManagedClients(service *managedclient.Service) *Server {
	s.managedClients = service
	return s
}
func (s *Server) EnableBuilderAPI(token []byte) *Server {
	s.builderToken = append([]byte(nil), token...)
	return s
}
func (s *Server) EnableBranding(dataDir string) *Server {
	s.brandingDir = filepath.Join(dataDir, "branding")
	return s
}
func (s *Server) EnableBackups(service *backupservice.Service) *Server { s.backups = service; return s }
func (s *Server) EnableRegistration(enabled bool, limiter *LoginLimiter) *Server {
	s.registrationEnabled, s.registrationLimiter = enabled, limiter
	return s
}
func (s *Server) EnableRuntimeConfig(service *runtimeconfig.Service) *Server {
	s.configuration = service
	return s
}

func (s *Server) EnableOperations(metricsToken []byte, trustedProxies []string) error {
	prefixes := make([]netip.Prefix, 0, len(trustedProxies))
	for _, value := range trustedProxies {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy %q: %w", value, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	s.metricsToken = append([]byte(nil), metricsToken...)
	s.trustedProxies = prefixes
	return nil
}

func (s *Server) Handler() http.Handler {
	return trustedProxyHeaders(s.trustedProxies, securityHeaders(recoverer(requestLogger(s.mux))))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /metrics", s.metrics)
	s.mux.HandleFunc("GET /api/login-options", s.loginOptions)
	s.mux.HandleFunc("GET /api/registration-options", s.registrationOptions)
	s.mux.HandleFunc("GET /api/branding/logo", s.brandingLogo)
	s.mux.HandleFunc("GET /api/avatar/{userID}", s.userAvatar)
	s.mux.HandleFunc("GET /avatar/{userID}", s.userAvatar)
	s.mux.HandleFunc("POST /api/register", s.register)
	s.mux.HandleFunc("POST /api/oidc/auth", s.oidcAuth)
	s.mux.HandleFunc("GET /api/oidc/auth-query", s.oidcAuthQuery)
	s.mux.HandleFunc("GET /api/oidc/callback", s.oidcCallback)
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("POST /api/heartbeat", s.clientHeartbeat)
	s.mux.HandleFunc("POST /api/sysinfo", s.clientSysinfo)
	s.mux.HandleFunc("POST /api/sysinfo_ver", s.clientSysinfoVersion)
	s.mux.HandleFunc("POST /api/audit/conn", s.clientConnectionAudit)
	s.mux.HandleFunc("POST /api/audit/file", s.clientFileAudit)
	s.mux.Handle("GET /api/users", s.requireAuth(http.HandlerFunc(s.clientUsers)))
	s.mux.Handle("GET /api/peers", s.requireAuth(http.HandlerFunc(s.clientPeers)))
	s.mux.Handle("GET /api/device-group/accessible", s.requireAuth(http.HandlerFunc(s.clientDeviceGroups)))
	s.mux.Handle("POST /api/logout", s.requireAuth(http.HandlerFunc(s.logout)))
	s.mux.Handle("POST /api/logout/all", s.requireAuth(http.HandlerFunc(s.logoutAll)))
	s.mux.Handle("GET /api/me", s.requireAuth(http.HandlerFunc(s.me)))
	s.mux.Handle("POST /api/me/avatar", s.requireAuth(http.HandlerFunc(s.uploadMyAvatar)))
	s.mux.Handle("DELETE /api/me/avatar", s.requireAuth(http.HandlerFunc(s.deleteMyAvatar)))
	s.mux.Handle("GET /api/api-tokens", s.requireAuth(http.HandlerFunc(s.listAPITokens)))
	s.mux.Handle("POST /api/api-tokens", s.requireAuth(http.HandlerFunc(s.createAPIToken)))
	s.mux.Handle("DELETE /api/api-tokens/{tokenID}", s.requireAuth(http.HandlerFunc(s.revokeAPIToken)))
	s.mux.HandleFunc("POST /api/devices/deploy", s.deployDevice)
	s.mux.Handle("POST /api/mfa/totp/enroll", s.requireAuth(http.HandlerFunc(s.enrollTOTP)))
	s.mux.Handle("POST /api/mfa/totp/confirm", s.requireAuth(http.HandlerFunc(s.confirmTOTP)))
	s.mux.Handle("DELETE /api/mfa/totp", s.requireAuth(http.HandlerFunc(s.disableTOTP)))
	s.mux.Handle("POST /api/mfa/totp/recovery-codes", s.requireAuth(http.HandlerFunc(s.regenerateRecoveryCodes)))
	s.mux.Handle("GET /api/oidc/identities", s.requireAuth(http.HandlerFunc(s.listMyOIDCIdentities)))
	s.mux.Handle("POST /api/oidc/link", s.requireAuth(http.HandlerFunc(s.beginOIDCLink)))
	s.mux.Handle("GET /api/oidc/link-query", s.requireAuth(http.HandlerFunc(s.queryOIDCLink)))
	s.mux.Handle("DELETE /api/oidc/identity", s.requireAuth(http.HandlerFunc(s.deleteMyOIDCIdentity)))
	s.mux.Handle("DELETE /api/admin/users/{userID}/mfa", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.adminResetTOTP)))
	s.mux.Handle("GET /api/admin/oidc/identities", s.requirePermission(domain.PermissionUsersRead, http.HandlerFunc(s.listAdminOIDCIdentities)))
	s.mux.Handle("DELETE /api/admin/oidc/identity", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.deleteAdminOIDCIdentity)))
	s.mux.Handle("GET /api/user/info", s.requireAuth(http.HandlerFunc(s.me)))
	s.mux.Handle("GET /api/currentUser", s.requireAuth(http.HandlerFunc(s.me)))
	s.mux.Handle("POST /api/currentUser", s.requireAuth(http.HandlerFunc(s.me)))
	s.mux.Handle("POST /api/sessions/{sessionID}/revoke", s.requireAuth(http.HandlerFunc(s.revokeSession)))
	s.mux.Handle("POST /api/admin/users/{userID}/disable", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.disableUser)))
	s.mux.Handle("POST /api/admin/users/{userID}/enable", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.enableUser)))
	s.mux.Handle("POST /api/admin/users/{userID}/force-relogin", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.forceRelogin)))
	s.mux.Handle("POST /api/admin/users/{userID}/approve", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.approveUser)))
	s.mux.Handle("POST /api/admin/users/{userID}/reject", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.rejectUser)))
	s.mux.Handle("GET /api/admin/users", s.requirePermission(domain.PermissionUsersRead, http.HandlerFunc(s.listUsers)))
	s.mux.Handle("POST /api/admin/users", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.createUser)))
	s.mux.Handle("PATCH /api/admin/users/{userID}", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.updateUser)))
	s.mux.Handle("DELETE /api/admin/users/{userID}", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.deleteUser)))
	s.mux.Handle("POST /api/admin/users/{userID}/password", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.setUserPassword)))
	s.mux.Handle("POST /api/admin/users/{userID}/avatar", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.uploadAdminUserAvatar)))
	s.mux.Handle("DELETE /api/admin/users/{userID}/avatar", s.requirePermission(domain.PermissionUsersWrite, http.HandlerFunc(s.deleteAdminUserAvatar)))
	s.mux.Handle("GET /api/admin/devices", s.requirePermission(domain.PermissionDevicesRead, http.HandlerFunc(s.listDevices)))
	s.mux.Handle("PATCH /api/admin/devices", s.requirePermission(domain.PermissionDevicesWrite, http.HandlerFunc(s.bulkUpdateDevices)))
	s.mux.Handle("PATCH /api/admin/devices/{rustdeskID}", s.requirePermission(domain.PermissionDevicesWrite, http.HandlerFunc(s.updateDevice)))
	s.mux.Handle("POST /api/admin/devices/{rustdeskID}/archive", s.requirePermission(domain.PermissionDevicesWrite, http.HandlerFunc(s.archiveDevice)))
	s.mux.Handle("POST /api/admin/devices/{rustdeskID}/restore", s.requirePermission(domain.PermissionDevicesWrite, http.HandlerFunc(s.restoreDevice)))
	s.mux.Handle("DELETE /api/admin/devices/{rustdeskID}", s.requirePermission(domain.PermissionDevicesWrite, http.HandlerFunc(s.deleteArchivedDevice)))
	s.mux.Handle("GET /api/admin/devices-export.csv", s.requirePermission(domain.PermissionDevicesRead, http.HandlerFunc(s.exportDevicesCSV)))
	s.mux.Handle("POST /api/admin/devices-import.csv", s.requirePermission(domain.PermissionDevicesWrite, http.HandlerFunc(s.importDevicesCSV)))
	s.mux.Handle("GET /api/admin/groups", s.requirePermission(domain.PermissionGroupsRead, http.HandlerFunc(s.listGroups)))
	s.mux.Handle("POST /api/admin/groups", s.requirePermission(domain.PermissionGroupsWrite, http.HandlerFunc(s.createGroup)))
	s.mux.Handle("PATCH /api/admin/groups/{groupID}", s.requirePermission(domain.PermissionGroupsWrite, http.HandlerFunc(s.updateGroup)))
	s.mux.Handle("DELETE /api/admin/groups/{groupID}", s.requirePermission(domain.PermissionGroupsWrite, http.HandlerFunc(s.deleteGroup)))
	s.mux.Handle("GET /api/admin/groups/{groupID}/members", s.requirePermission(domain.PermissionGroupsRead, http.HandlerFunc(s.listGroupMembers)))
	s.mux.Handle("PUT /api/admin/groups/{groupID}/members/{userID}", s.requirePermission(domain.PermissionGroupsWrite, http.HandlerFunc(s.addGroupMember)))
	s.mux.Handle("DELETE /api/admin/groups/{groupID}/members/{userID}", s.requirePermission(domain.PermissionGroupsWrite, http.HandlerFunc(s.removeGroupMember)))
	s.mux.Handle("GET /api/admin/sessions", s.requirePermission(domain.PermissionSessionsRead, http.HandlerFunc(s.listSessions)))
	s.mux.Handle("GET /api/admin/sessions/query", s.requirePermission(domain.PermissionSessionsRead, http.HandlerFunc(s.querySessions)))
	s.mux.Handle("GET /api/admin/sessions/summary", s.requirePermission(domain.PermissionSessionsRead, http.HandlerFunc(s.sessionSummary)))
	s.mux.Handle("POST /api/admin/sessions/revoke", s.requirePermission(domain.PermissionSessionsRevoke, http.HandlerFunc(s.bulkRevokeSessions)))
	s.mux.Handle("GET /api/admin/connections", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.liveConnections)))
	s.mux.Handle("POST /api/admin/connections/contain", s.requirePermission(domain.PermissionSessionsRevoke, http.HandlerFunc(s.containLiveConnection)))
	s.mux.Handle("GET /api/admin/audit", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.listAudit)))
	s.mux.Handle("GET /api/admin/audit/query", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.queryAudit)))
	s.mux.Handle("GET /api/admin/audit/summary", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.auditSummary)))
	s.mux.Handle("GET /api/admin/audit/export.csv", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.exportAuditCSV)))
	s.mux.Handle("GET /api/admin/notifications", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.listNotifications)))
	s.mux.Handle("POST /api/admin/notifications/read-all", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.markAllNotificationsRead)))
	s.mux.Handle("POST /api/admin/notifications/{notificationID}/read", s.requirePermission(domain.PermissionAuditRead, http.HandlerFunc(s.markNotificationRead)))
	s.mux.Handle("GET /api/admin/automation/rules", s.requirePermission(domain.PermissionAutomationRead, http.HandlerFunc(s.listAutomationRules)))
	s.mux.Handle("POST /api/admin/automation/rules", s.requirePermission(domain.PermissionAutomationWrite, http.HandlerFunc(s.createAutomationRule)))
	s.mux.Handle("PATCH /api/admin/automation/rules/{ruleID}", s.requirePermission(domain.PermissionAutomationWrite, http.HandlerFunc(s.updateAutomationRule)))
	s.mux.Handle("DELETE /api/admin/automation/rules/{ruleID}", s.requirePermission(domain.PermissionAutomationWrite, http.HandlerFunc(s.deleteAutomationRule)))
	s.mux.Handle("GET /api/admin/automation/runs", s.requirePermission(domain.PermissionAutomationRead, http.HandlerFunc(s.listAutomationRuns)))
	s.mux.Handle("GET /api/admin/cluster", s.requirePermission(domain.PermissionInfrastructureRead, http.HandlerFunc(s.clusterState)))
	s.mux.Handle("GET /api/admin/support-bundle", s.requirePermission(domain.PermissionInfrastructureRead, http.HandlerFunc(s.supportBundle)))
	s.mux.Handle("GET /api/admin/settings", s.requirePermission(domain.PermissionSettingsRead, http.HandlerFunc(s.settings)))
	s.mux.Handle("PATCH /api/admin/settings", s.requirePermission(domain.PermissionSettingsWrite, http.HandlerFunc(s.updateSettings)))
	s.mux.Handle("POST /api/admin/settings/logo", s.requirePermission(domain.PermissionSettingsWrite, http.HandlerFunc(s.uploadBrandingLogo)))
	s.mux.Handle("DELETE /api/admin/settings/logo", s.requirePermission(domain.PermissionSettingsWrite, http.HandlerFunc(s.deleteBrandingLogo)))
	s.mux.Handle("POST /api/admin/settings/avatar", s.requirePermission(domain.PermissionSettingsWrite, http.HandlerFunc(s.uploadGlobalAvatar)))
	s.mux.Handle("DELETE /api/admin/settings/avatar", s.requirePermission(domain.PermissionSettingsWrite, http.HandlerFunc(s.deleteGlobalAvatar)))
	s.mux.Handle("GET /api/admin/backups/sqlite", s.requirePermission(domain.PermissionBackupRead, http.HandlerFunc(s.backupSQLite)))
	s.mux.Handle("POST /api/admin/backups/sqlite/inspect", s.requirePermission(domain.PermissionBackupRead, http.HandlerFunc(s.inspectSQLiteBackup)))
	s.mux.Handle("GET /api/admin/backups", s.requirePermission(domain.PermissionBackupRead, http.HandlerFunc(s.listManagedBackups)))
	s.mux.Handle("POST /api/admin/backups", s.requirePermission(domain.PermissionBackupWrite, http.HandlerFunc(s.createManagedBackup)))
	s.mux.Handle("GET /api/admin/backups/restore", s.requirePermission(domain.PermissionBackupRead, http.HandlerFunc(s.restoreStatus)))
	s.mux.Handle("POST /api/admin/backups/restore", s.requirePermission(domain.PermissionBackupWrite, http.HandlerFunc(s.stageRestore)))
	s.mux.Handle("DELETE /api/admin/backups/restore", s.requirePermission(domain.PermissionBackupWrite, http.HandlerFunc(s.cancelRestore)))
	s.mux.Handle("GET /api/admin/backups/{name}", s.requirePermission(domain.PermissionBackupRead, http.HandlerFunc(s.downloadManagedBackup)))
	s.mux.Handle("DELETE /api/admin/backups/{name}", s.requirePermission(domain.PermissionBackupWrite, http.HandlerFunc(s.deleteManagedBackup)))
	s.mux.Handle("GET /api/admin/infrastructure", s.requirePermission(domain.PermissionInfrastructureRead, http.HandlerFunc(s.infrastructure)))
	s.mux.Handle("GET /api/admin/presence", s.requirePermission(domain.PermissionUsersRead, http.HandlerFunc(s.userPresence)))
	s.mux.Handle("GET /api/admin/preferences/dashboard", s.requireAuth(http.HandlerFunc(s.getDashboardLayout)))
	s.mux.Handle("PUT /api/admin/preferences/dashboard", s.requireAuth(http.HandlerFunc(s.putDashboardLayout)))
	s.mux.Handle("GET /api/admin/diagnostics", s.requirePermission(domain.PermissionInfrastructureRead, http.HandlerFunc(s.diagnostics)))
	s.mux.Handle("GET /api/admin/infrastructure/commands", s.requirePermission(domain.PermissionInfrastructureRead, http.HandlerFunc(s.listServiceCommands)))
	s.mux.Handle("POST /api/admin/infrastructure/commands", s.requirePermission(domain.PermissionInfrastructureWrite, http.HandlerFunc(s.createServiceCommand)))
	s.mux.Handle("GET /api/admin/address-books", s.requirePermission(domain.PermissionAddressBooksRead, http.HandlerFunc(s.listAddressBooks)))
	s.mux.Handle("POST /api/admin/address-books", s.requirePermission(domain.PermissionAddressBooksWrite, http.HandlerFunc(s.createAddressBook)))
	s.mux.Handle("PATCH /api/admin/address-books/{bookID}", s.requirePermission(domain.PermissionAddressBooksWrite, http.HandlerFunc(s.updateAddressBook)))
	s.mux.Handle("DELETE /api/admin/address-books/{bookID}", s.requirePermission(domain.PermissionAddressBooksWrite, http.HandlerFunc(s.deleteAddressBook)))
	s.mux.Handle("GET /api/admin/address-books/{bookID}/entries", s.requirePermission(domain.PermissionAddressBooksRead, http.HandlerFunc(s.listAddressBookEntries)))
	s.mux.Handle("POST /api/admin/address-books/{bookID}/entries", s.requirePermission(domain.PermissionAddressBooksWrite, http.HandlerFunc(s.createAddressBookEntry)))
	s.mux.Handle("PATCH /api/admin/address-books/{bookID}/entries/{entryID}", s.requirePermission(domain.PermissionAddressBooksWrite, http.HandlerFunc(s.updateAddressBookEntry)))
	s.mux.Handle("DELETE /api/admin/address-books/{bookID}/entries/{entryID}", s.requirePermission(domain.PermissionAddressBooksWrite, http.HandlerFunc(s.deleteAddressBookEntry)))
	s.mux.Handle("GET /api/address-books", s.requireAuth(http.HandlerFunc(s.listAddressBooks)))
	s.mux.Handle("POST /api/address-books", s.requireAuth(http.HandlerFunc(s.createAddressBook)))
	s.mux.Handle("PATCH /api/address-books/{bookID}", s.requireAuth(http.HandlerFunc(s.updateAddressBook)))
	s.mux.Handle("DELETE /api/address-books/{bookID}", s.requireAuth(http.HandlerFunc(s.deleteAddressBook)))
	s.mux.Handle("GET /api/address-books/{bookID}/entries", s.requireAuth(http.HandlerFunc(s.listAddressBookEntries)))
	s.mux.Handle("POST /api/address-books/{bookID}/entries", s.requireAuth(http.HandlerFunc(s.createAddressBookEntry)))
	s.mux.Handle("PATCH /api/address-books/{bookID}/entries/{entryID}", s.requireAuth(http.HandlerFunc(s.updateAddressBookEntry)))
	s.mux.Handle("DELETE /api/address-books/{bookID}/entries/{entryID}", s.requireAuth(http.HandlerFunc(s.deleteAddressBookEntry)))
	s.mux.Handle("GET /api/address-books/{bookID}/grants", s.requireAuth(http.HandlerFunc(s.listAddressBookGrants)))
	s.mux.Handle("PUT /api/address-books/{bookID}/grants", s.requireAuth(http.HandlerFunc(s.putAddressBookGrant)))
	s.mux.Handle("DELETE /api/address-books/{bookID}/grants/{grantID}", s.requireAuth(http.HandlerFunc(s.deleteAddressBookGrant)))
	s.mux.Handle("GET /api/ab", s.requireAuth(http.HandlerFunc(s.legacyAddressBook)))
	s.mux.Handle("POST /api/ab", s.requireAuth(http.HandlerFunc(s.updateLegacyAddressBook)))
	s.mux.Handle("POST /api/ab/get", s.requireAuth(http.HandlerFunc(s.legacyAddressBook)))
	s.mux.Handle("POST /api/ab/personal", s.requireAuth(http.HandlerFunc(s.clientPersonalAddressBook)))
	s.mux.Handle("POST /api/ab/settings", s.requireAuth(http.HandlerFunc(s.clientAddressBookSettings)))
	s.mux.Handle("POST /api/ab/shared/profiles", s.requireAuth(http.HandlerFunc(s.clientSharedAddressBooks)))
	s.mux.Handle("POST /api/ab/shared-profiles", s.requireAuth(http.HandlerFunc(s.clientSharedAddressBooks)))
	s.mux.Handle("GET /api/ab/peers", s.requireAuth(http.HandlerFunc(s.clientAddressBookPeers)))
	s.mux.Handle("POST /api/ab/peers", s.requireAuth(http.HandlerFunc(s.clientAddressBookPeers)))
	s.mux.Handle("POST /api/ab/tags/{bookID}", s.requireAuth(http.HandlerFunc(s.clientAddressBookTags)))
	s.mux.Handle("POST /api/ab/tag/add/{bookID}", s.requireAuth(http.HandlerFunc(s.clientAddAddressBookTag)))
	s.mux.Handle("PUT /api/ab/tag/rename/{bookID}", s.requireAuth(http.HandlerFunc(s.clientRenameAddressBookTag)))
	s.mux.Handle("PUT /api/ab/tag/update/{bookID}", s.requireAuth(http.HandlerFunc(s.clientUpdateAddressBookTag)))
	s.mux.Handle("DELETE /api/ab/tag/{bookID}", s.requireAuth(http.HandlerFunc(s.clientDeleteAddressBookTags)))
	s.mux.Handle("POST /api/ab/peer/add/{bookID}", s.requireAuth(http.HandlerFunc(s.clientAddAddressBookPeer)))
	s.mux.Handle("PUT /api/ab/peer/update/{bookID}", s.requireAuth(http.HandlerFunc(s.clientUpdateAddressBookPeer)))
	s.mux.Handle("DELETE /api/ab/peer/{bookID}", s.requireAuth(http.HandlerFunc(s.clientDeleteAddressBookPeer)))
	s.mux.Handle("GET /api/admin/relay-servers", s.requirePermission(domain.PermissionRelaysRead, http.HandlerFunc(s.listRelayServers)))
	s.mux.Handle("POST /api/admin/relay-servers", s.requirePermission(domain.PermissionRelaysWrite, http.HandlerFunc(s.createRelayServer)))
	s.mux.Handle("PATCH /api/admin/relay-servers/{relayID}", s.requirePermission(domain.PermissionRelaysWrite, http.HandlerFunc(s.updateRelayServer)))
	s.mux.Handle("DELETE /api/admin/relay-servers/{relayID}", s.requirePermission(domain.PermissionRelaysWrite, http.HandlerFunc(s.deleteRelayServer)))
	s.mux.Handle("GET /api/admin/relay-servers/{relayID}/metrics", s.requirePermission(domain.PermissionRelaysRead, http.HandlerFunc(s.listRelayMetrics)))
	s.mux.Handle("GET /api/admin/acl", s.requirePermission(domain.PermissionACLRead, http.HandlerFunc(s.listACLRules)))
	s.mux.Handle("POST /api/admin/acl/evaluate", s.requirePermission(domain.PermissionACLRead, http.HandlerFunc(s.evaluateACL)))
	s.mux.Handle("POST /api/admin/acl", s.requirePermission(domain.PermissionACLWrite, http.HandlerFunc(s.createACLRule)))
	s.mux.Handle("PATCH /api/admin/acl/{ruleID}", s.requirePermission(domain.PermissionACLWrite, http.HandlerFunc(s.updateACLRule)))
	s.mux.Handle("DELETE /api/admin/acl/{ruleID}", s.requirePermission(domain.PermissionACLWrite, http.HandlerFunc(s.deleteACLRule)))
	s.mux.Handle("GET /api/admin/strategies", s.requirePermission(domain.PermissionStrategiesRead, http.HandlerFunc(s.listStrategies)))
	s.mux.Handle("GET /api/admin/strategies/schema", s.requirePermission(domain.PermissionStrategiesRead, http.HandlerFunc(s.strategySchema)))
	s.mux.Handle("POST /api/admin/strategies/evaluate", s.requirePermission(domain.PermissionStrategiesRead, http.HandlerFunc(s.evaluateStrategy)))
	s.mux.Handle("POST /api/admin/strategies", s.requirePermission(domain.PermissionStrategiesWrite, http.HandlerFunc(s.createStrategy)))
	s.mux.Handle("PATCH /api/admin/strategies/{strategyID}", s.requirePermission(domain.PermissionStrategiesWrite, http.HandlerFunc(s.updateStrategy)))
	s.mux.Handle("DELETE /api/admin/strategies/{strategyID}", s.requirePermission(domain.PermissionStrategiesWrite, http.HandlerFunc(s.deleteStrategy)))
	s.mux.Handle("GET /api/admin/roles", s.requirePermission(domain.PermissionRolesRead, http.HandlerFunc(s.listRoles)))
	s.mux.Handle("POST /api/admin/roles", s.requirePermission(domain.PermissionRolesWrite, http.HandlerFunc(s.createRole)))
	s.mux.Handle("PATCH /api/admin/roles/{roleID}", s.requirePermission(domain.PermissionRolesWrite, http.HandlerFunc(s.updateRole)))
	s.mux.Handle("DELETE /api/admin/roles/{roleID}", s.requirePermission(domain.PermissionRolesWrite, http.HandlerFunc(s.deleteRole)))
	s.mux.Handle("GET /api/admin/permissions", s.requirePermission(domain.PermissionRolesRead, http.HandlerFunc(s.listPermissions)))
	s.mux.Handle("GET /api/admin/webhooks", s.requirePermission(domain.PermissionWebhooksRead, http.HandlerFunc(s.listWebhooks)))
	s.mux.Handle("POST /api/admin/webhooks", s.requirePermission(domain.PermissionWebhooksWrite, http.HandlerFunc(s.createWebhook)))
	s.mux.Handle("PATCH /api/admin/webhooks/{webhookID}", s.requirePermission(domain.PermissionWebhooksWrite, http.HandlerFunc(s.updateWebhook)))
	s.mux.Handle("DELETE /api/admin/webhooks/{webhookID}", s.requirePermission(domain.PermissionWebhooksWrite, http.HandlerFunc(s.deleteWebhook)))
	s.mux.Handle("GET /api/admin/webhooks/{webhookID}/deliveries", s.requirePermission(domain.PermissionWebhooksRead, http.HandlerFunc(s.listWebhookDeliveries)))
	s.mux.Handle("GET /api/admin/client-profiles", s.requirePermission(domain.PermissionClientProfilesRead, http.HandlerFunc(s.listClientProfiles)))
	s.mux.Handle("POST /api/admin/client-profiles", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.createClientProfile)))
	s.mux.Handle("PATCH /api/admin/client-profiles/{profileID}", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.updateClientProfile)))
	s.mux.Handle("DELETE /api/admin/client-profiles/{profileID}", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.deleteClientProfile)))
	s.mux.Handle("GET /api/admin/client-profiles/{profileID}/bundle", s.requirePermission(domain.PermissionClientProfilesRead, http.HandlerFunc(s.clientProfileBundle)))
	s.mux.Handle("GET /api/admin/client-profile-assignments", s.requirePermission(domain.PermissionClientProfilesRead, http.HandlerFunc(s.listClientProfileAssignments)))
	s.mux.Handle("POST /api/admin/client-profile-assignments", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.createClientProfileAssignment)))
	s.mux.Handle("DELETE /api/admin/client-profile-assignments/{assignmentID}", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.deleteClientProfileAssignment)))
	s.mux.Handle("GET /api/admin/client-builds", s.requirePermission(domain.PermissionClientProfilesRead, http.HandlerFunc(s.listClientBuilds)))
	s.mux.Handle("GET /api/admin/builder-workers", s.requirePermission(domain.PermissionClientProfilesRead, http.HandlerFunc(s.listBuilderWorkers)))
	s.mux.Handle("POST /api/admin/client-builds", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.createClientBuild)))
	s.mux.Handle("POST /api/admin/client-builds/{buildID}/cancel", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.cancelClientBuild)))
	s.mux.Handle("POST /api/admin/client-builds/{buildID}/retry", s.requirePermission(domain.PermissionClientProfilesWrite, http.HandlerFunc(s.retryClientBuild)))
	s.mux.Handle("GET /api/admin/client-builds/{buildID}/artifact", s.requirePermission(domain.PermissionClientProfilesRead, http.HandlerFunc(s.clientBuildArtifact)))
	s.mux.Handle("GET /internal/v1/client-profile/{rustdeskID}", s.requireInternal(http.HandlerFunc(s.effectiveClientProfile)))
	s.mux.Handle("POST /internal/v1/client-builds/claim", s.requireBuilderWorker(http.HandlerFunc(s.claimClientBuild)))
	s.mux.Handle("POST /internal/v1/builders/register", s.requireBuilder(http.HandlerFunc(s.registerBuilderWorker)))
	s.mux.Handle("POST /internal/v1/builders/heartbeat", s.requireBuilderWorker(http.HandlerFunc(s.registerBuilderWorker)))
	s.mux.Handle("POST /internal/v1/client-builds/{buildID}/heartbeat", s.requireBuilderWorker(http.HandlerFunc(s.heartbeatClientBuild)))
	s.mux.Handle("GET /internal/v1/client-builds/{buildID}/payload", s.requireBuilderWorker(http.HandlerFunc(s.clientBuildPayload)))
	s.mux.Handle("POST /internal/v1/client-builds/{buildID}/complete", s.requireBuilderWorker(http.HandlerFunc(s.completeClientBuild)))
	s.mux.Handle("POST /internal/v1/client-builds/{buildID}/fail", s.requireBuilderWorker(http.HandlerFunc(s.failClientBuild)))
	s.mux.Handle("GET /internal/v1/auth/snapshot", s.requireInternal(http.HandlerFunc(s.authSnapshot)))
	s.mux.Handle("GET /internal/v1/relay/select", s.requireInternal(http.HandlerFunc(s.selectRelay)))
	s.mux.Handle("GET /internal/v1/auth/events", s.requireInternal(http.HandlerFunc(s.authEvents)))
	s.mux.Handle("POST /internal/v1/audit/connections", s.requireInternal(http.HandlerFunc(s.connectionAudit)))
	s.mux.Handle("POST /internal/v1/devices/heartbeat", s.requireInternal(http.HandlerFunc(s.deviceHeartbeat)))
	s.mux.Handle("POST /internal/v1/relay/telemetry", s.requireInternal(http.HandlerFunc(s.relayTelemetry)))
	s.mux.Handle("POST /internal/v1/services/heartbeat", s.requireInternal(http.HandlerFunc(s.serviceHeartbeat)))
	s.mux.Handle("GET /", webui.Handler())
}

type relayTelemetryRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Hostname    string `json:"hostname"`
	Port        int    `json:"port"`
	Region      string `json:"region"`
	Connections int    `json:"connections"`
	Bandwidth   int64  `json:"bandwidth"`
}

func (s *Server) relayTelemetry(response http.ResponseWriter, request *http.Request) {
	var input relayTelemetryRequest
	if err := decodeJSON(request, &input, 16<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid relay telemetry")
		return
	}
	input.ID, input.Name = strings.TrimSpace(input.ID), strings.TrimSpace(input.Name)
	input.Hostname, input.Region = strings.TrimSpace(input.Hostname), strings.TrimSpace(input.Region)
	if input.ID == "" || len(input.ID) > 128 || len(input.Name) < 2 || len(input.Name) > 128 ||
		input.Hostname == "" || len(input.Hostname) > 253 || input.Port < 1 || input.Port > 65535 ||
		len(input.Region) > 64 || input.Connections < 0 || input.Bandwidth < 0 {
		writeError(response, http.StatusBadRequest, "invalid relay telemetry")
		return
	}
	value, err := s.repository.UpsertRelayTelemetry(request.Context(), domain.RelayServer{
		ID: input.ID, Name: input.Name, Hostname: input.Hostname, Port: input.Port, Region: input.Region,
		Connections: input.Connections, Bandwidth: input.Bandwidth, UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "relay telemetry unavailable")
		return
	}
	s.hub.Publish(events.RelayUpdated, value)
	_ = s.repository.AppendRelayMetric(request.Context(), domain.RelayMetric{RelayID: value.ID, RecordedAt: value.UpdatedAt, Health: "healthy", LatencyMS: value.LatencyMS, Connections: value.Connections, Bandwidth: value.Bandwidth})
	s.runtime.heartbeat("hbbr", value.ID, 0)
	writeJSON(response, http.StatusOK, value)
}

type serviceHeartbeatRequest struct {
	Service              string   `json:"service"`
	InstanceID           string   `json:"instance_id"`
	OnlinePeers          int      `json:"online_peers"`
	AcknowledgedCommands []string `json:"acknowledged_commands"`
}

func (s *Server) serviceHeartbeat(response http.ResponseWriter, request *http.Request) {
	var input serviceHeartbeatRequest
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid service heartbeat")
		return
	}
	input.Service, input.InstanceID = strings.TrimSpace(input.Service), strings.TrimSpace(input.InstanceID)
	if input.Service != "hbbs" || input.InstanceID == "" || len(input.InstanceID) > 128 || input.OnlinePeers < 0 {
		writeError(response, http.StatusBadRequest, "invalid service heartbeat")
		return
	}
	s.runtime.heartbeat(input.Service, input.InstanceID, input.OnlinePeers)
	commands := s.runtime.heartbeatCommands(input.Service, input.InstanceID, input.AcknowledgedCommands)
	writeJSON(response, http.StatusOK, map[string]any{"commands": commands})
}

func (s *Server) listServiceCommands(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, s.runtime.listCommands())
}

func (s *Server) createServiceCommand(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Service        string `json:"service"`
		TargetInstance string `json:"target_instance"`
		Type           string `json:"type"`
	}
	if decodeJSON(request, &input, 8<<10) != nil {
		writeError(response, 400, "invalid command")
		return
	}
	input.Service, input.TargetInstance, input.Type = strings.TrimSpace(input.Service), strings.TrimSpace(input.TargetInstance), strings.TrimSpace(input.Type)
	if input.Service != "hbbs" || input.Type != "reconcile_auth" || (input.TargetInstance == "" || len(input.TargetInstance) > 128) {
		writeError(response, 400, "invalid command")
		return
	}
	command := s.runtime.enqueueCommand(input.Service, input.TargetInstance, input.Type)
	principal, _ := principalFrom(request.Context())
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "server_control_command", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "queued", Metadata: map[string]any{"command_id": command.ID, "service": command.Service, "target": command.TargetInstance, "command": command.Type}})
	writeJSON(response, http.StatusAccepted, command)
}

func (s *Server) selectRelay(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListRelayServers(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "relay selection unavailable")
		return
	}
	value, err := relayservice.Select(values, strings.TrimSpace(request.URL.Query().Get("region")))
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusServiceUnavailable, "no healthy relay available")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"id": value.ID, "relay_server": net.JoinHostPort(value.Hostname, strconv.Itoa(value.Port)), "region": value.Region, "latency_ms": value.LatencyMS})
}

type deviceHeartbeatRequest struct {
	RustDeskID string `json:"rustdesk_id"`
	ClientUUID string `json:"client_uuid"`
	LastSeenIP string `json:"last_seen_ip"`
}

func (s *Server) deviceHeartbeat(response http.ResponseWriter, request *http.Request) {
	var input deviceHeartbeatRequest
	if err := decodeJSON(request, &input, 8<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid device heartbeat")
		return
	}
	input.RustDeskID = strings.TrimSpace(input.RustDeskID)
	input.LastSeenIP = strings.TrimSpace(input.LastSeenIP)
	if len(input.RustDeskID) < 3 || len(input.RustDeskID) > 64 || len(input.ClientUUID) > 256 ||
		(len(input.LastSeenIP) > 0 && net.ParseIP(input.LastSeenIP) == nil) {
		writeError(response, http.StatusBadRequest, "invalid device heartbeat")
		return
	}
	now := time.Now().UTC()
	if err := s.repository.UpsertDevice(request.Context(), domain.Device{
		RustDeskID: input.RustDeskID, ClientUUID: input.ClientUUID, Online: true,
		LastSeenIP: input.LastSeenIP, LastSeen: now, CreatedAt: now,
	}); err != nil {
		writeError(response, http.StatusInternalServerError, "device heartbeat failed")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAddressBooks(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	values, err := s.addressBooks.List(request.Context(), principal.User)
	if err != nil {
		writeError(response, 500, "failed to list address books")
		return
	}
	writeJSON(response, 200, values)
}
func (s *Server) listAddressBookEntries(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, request.PathValue("bookID"), addressbookservice.PermissionRead); err != nil {
		writeAddressBookError(response, err)
		return
	}
	values, err := s.repository.ListAddressBookEntries(request.Context(), request.PathValue("bookID"))
	if err != nil {
		writeError(response, 500, "failed to list address book entries")
		return
	}
	writeJSON(response, 200, values)
}
func (s *Server) createAddressBookEntry(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, request.PathValue("bookID"), addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var v domain.AddressBookEntry
	if decodeJSON(request, &v, 16<<10) != nil {
		writeError(response, 400, "invalid address book entry")
		return
	}
	v.RustDeskID = strings.TrimSpace(v.RustDeskID)
	v.Alias = strings.TrimSpace(v.Alias)
	v.Folder = strings.TrimSpace(v.Folder)
	if len(v.RustDeskID) < 3 || len(v.RustDeskID) > 64 || len(v.Alias) > 128 || len(v.Folder) > 128 {
		writeError(response, 400, "invalid address book entry")
		return
	}
	v.ID = uuid.NewString()
	v.AddressBookID = request.PathValue("bookID")
	v.CreatedAt = time.Now().UTC()
	if s.repository.CreateAddressBookEntry(request.Context(), v) != nil {
		writeError(response, 409, "device is already in this address book")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_update", ActorUserID: principal.User.ID, TargetRustDeskID: v.RustDeskID, Result: "success"})
	writeJSON(response, 201, v)
}
func (s *Server) createAddressBook(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var v domain.AddressBook
	if decodeJSON(request, &v, 16<<10) != nil {
		writeError(response, 400, "invalid address book")
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	if len(v.Name) < 2 || len(v.Name) > 128 || (v.Kind != "personal" && v.Kind != "shared") {
		writeError(response, 400, "invalid address book")
		return
	}
	if v.Kind == "shared" && principal.User.Role != domain.RoleAdmin {
		writeError(response, http.StatusForbidden, "only administrators can create shared address books")
		return
	}
	now := time.Now().UTC()
	v.ID = uuid.NewString()
	v.OwnerUserID = principal.User.ID
	v.Permission = addressbookservice.PermissionManage
	v.CanManage = true
	v.CreatedAt = now
	v.UpdatedAt = now
	if s.repository.CreateAddressBook(request.Context(), v) != nil {
		writeError(response, 500, "failed to create address book")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_create", ActorUserID: principal.User.ID, Result: "success"})
	writeJSON(response, 201, v)
}
func validAddressBook(v *domain.AddressBook) bool {
	v.Name = strings.TrimSpace(v.Name)
	return len(v.Name) >= 2 && len(v.Name) <= 128 && (v.Kind == "personal" || v.Kind == "shared")
}
func statusForStore(err error) int {
	if errors.Is(err, domain.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
func validAddressBookEntry(v *domain.AddressBookEntry) bool {
	v.RustDeskID, v.Alias, v.Folder = strings.TrimSpace(v.RustDeskID), strings.TrimSpace(v.Alias), strings.TrimSpace(v.Folder)
	return len(v.RustDeskID) >= 3 && len(v.RustDeskID) <= 64 && len(v.Alias) <= 128 && len(v.Folder) <= 128
}
func (s *Server) updateAddressBook(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	existing, err := s.addressBooks.Authorize(request.Context(), principal.User, request.PathValue("bookID"), addressbookservice.PermissionManage)
	if err != nil {
		writeAddressBookError(response, err)
		return
	}
	var v domain.AddressBook
	if decodeJSON(request, &v, 16<<10) != nil || !validAddressBook(&v) {
		writeError(response, 400, "invalid address book")
		return
	}
	if principal.User.Role != domain.RoleAdmin && v.Kind != existing.Kind {
		writeError(response, http.StatusForbidden, "only administrators can change address book type")
		return
	}
	v.ID, v.OwnerUserID, v.CreatedAt, v.UpdatedAt = existing.ID, existing.OwnerUserID, existing.CreatedAt, time.Now().UTC()
	v.Permission, v.CanManage = addressbookservice.PermissionManage, true
	if err := s.repository.UpdateAddressBook(request.Context(), v); err != nil {
		writeError(response, statusForStore(err), "address book not found")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_update", ActorUserID: principal.User.ID, Result: "success"})
	writeJSON(response, 200, v)
}
func (s *Server) deleteAddressBook(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, request.PathValue("bookID"), addressbookservice.PermissionManage); err != nil {
		writeAddressBookError(response, err)
		return
	}
	if err := s.repository.DeleteAddressBook(request.Context(), request.PathValue("bookID")); err != nil {
		writeError(response, statusForStore(err), "address book not found")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_delete", ActorUserID: principal.User.ID, Result: "success"})
	response.WriteHeader(http.StatusNoContent)
}
func (s *Server) updateAddressBookEntry(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, request.PathValue("bookID"), addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	var v domain.AddressBookEntry
	if decodeJSON(request, &v, 16<<10) != nil || !validAddressBookEntry(&v) {
		writeError(response, 400, "invalid address book entry")
		return
	}
	v.ID, v.AddressBookID = request.PathValue("entryID"), request.PathValue("bookID")
	if err := s.repository.UpdateAddressBookEntry(request.Context(), v); err != nil {
		writeError(response, statusForStore(err), "address book entry not found")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_entry_update", ActorUserID: principal.User.ID, TargetRustDeskID: v.RustDeskID, Result: "success"})
	writeJSON(response, 200, v)
}
func (s *Server) deleteAddressBookEntry(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if _, err := s.addressBooks.Authorize(request.Context(), principal.User, request.PathValue("bookID"), addressbookservice.PermissionWrite); err != nil {
		writeAddressBookError(response, err)
		return
	}
	if err := s.repository.DeleteAddressBookEntry(request.Context(), request.PathValue("bookID"), request.PathValue("entryID")); err != nil {
		writeError(response, statusForStore(err), "address book entry not found")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "address_book_entry_delete", ActorUserID: principal.User.ID, Result: "success"})
	response.WriteHeader(http.StatusNoContent)
}
func writeAddressBookError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, addressbookservice.ErrForbidden):
		writeError(response, http.StatusForbidden, "address book access denied")
	case errors.Is(err, domain.ErrNotFound):
		writeError(response, http.StatusNotFound, "address book not found")
	default:
		writeError(response, http.StatusInternalServerError, "address book unavailable")
	}
}
func validRelay(v *domain.RelayServer) bool {
	v.Name, v.Hostname, v.Region = strings.TrimSpace(v.Name), strings.TrimSpace(v.Hostname), strings.TrimSpace(v.Region)
	return len(v.Name) >= 2 && len(v.Name) <= 128 && len(v.Hostname) >= 1 && len(v.Hostname) <= 253 && v.Port >= 1 && v.Port <= 65535 && len(v.Region) <= 64
}
func (s *Server) listRelayServers(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListRelayServers(request.Context())
	if err != nil {
		writeError(response, 500, "failed to list relay servers")
		return
	}
	writeJSON(response, 200, values)
}
func (s *Server) createRelayServer(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var v domain.RelayServer
	if decodeJSON(request, &v, 16<<10) != nil || !validRelay(&v) {
		writeError(response, 400, "invalid relay server")
		return
	}
	now := time.Now().UTC()
	v.ID = uuid.NewString()
	v.Enabled = true
	v.Health = "unknown"
	v.CreatedAt, v.UpdatedAt = now, now
	if err := s.repository.CreateRelayServer(request.Context(), v); err != nil {
		writeError(response, 409, "relay server already exists")
		return
	}
	s.hub.Publish(events.RelayUpdated, v)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "relay_create", ActorUserID: principal.User.ID, Result: "success"})
	writeJSON(response, 201, v)
}
func (s *Server) updateRelayServer(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var v domain.RelayServer
	if decodeJSON(request, &v, 16<<10) != nil || !validRelay(&v) {
		writeError(response, 400, "invalid relay server")
		return
	}
	v.ID = request.PathValue("relayID")
	v.Health = "unknown"
	v.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateRelayServer(request.Context(), v); err != nil {
		writeError(response, statusForStore(err), "relay server not found")
		return
	}
	if values, err := s.repository.ListRelayServers(request.Context()); err == nil {
		for _, current := range values {
			if current.ID == v.ID {
				v = current
				break
			}
		}
	}
	s.hub.Publish(events.RelayUpdated, v)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "relay_update", ActorUserID: principal.User.ID, Result: "success"})
	writeJSON(response, 200, v)
}
func (s *Server) deleteRelayServer(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if err := s.repository.DeleteRelayServer(request.Context(), request.PathValue("relayID")); err != nil {
		writeError(response, statusForStore(err), "relay server not found")
		return
	}
	s.hub.Publish(events.RelayUpdated, domain.RelayServer{ID: request.PathValue("relayID"), Deleted: true})
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "relay_delete", ActorUserID: principal.User.ID, Result: "success"})
	response.WriteHeader(http.StatusNoContent)
}
func (s *Server) listRelayMetrics(response http.ResponseWriter, request *http.Request) {
	hours, err := strconv.Atoi(request.URL.Query().Get("hours"))
	if err != nil || hours < 1 || hours > 720 {
		hours = 24
	}
	values, err := s.repository.ListRelayMetrics(request.Context(), request.PathValue("relayID"), time.Now().UTC().Add(-time.Duration(hours)*time.Hour), 2000)
	if err != nil {
		writeError(response, 500, "relay metrics unavailable")
		return
	}
	writeJSON(response, 200, values)
}
func (s *Server) listACLRules(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListACLRules(request.Context())
	if err != nil {
		writeError(response, 500, "failed to list ACL rules")
		return
	}
	writeJSON(response, 200, values)
}
func (s *Server) evaluateACL(response http.ResponseWriter, request *http.Request) {
	var input struct {
		UserID     string `json:"user_id"`
		TargetID   string `json:"target_id"`
		Permission string `json:"permission"`
	}
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, 400, "invalid request")
		return
	}
	allowedPermissions := map[string]bool{"remote_control": true, "file_transfer": true, "clipboard": true, "tcp_tunnel": true, "terminal": true, "audio": true, "camera": true, "view": true, "manage": true}
	user, err := s.repository.FindUserByID(request.Context(), input.UserID)
	if err != nil {
		writeError(response, 404, "user not found")
		return
	}
	if !allowedPermissions[input.Permission] {
		writeError(response, 400, "invalid permission")
		return
	}
	devices, err := s.repository.ListDevices(request.Context())
	if err != nil {
		writeError(response, 500, "devices unavailable")
		return
	}
	var device domain.Device
	found := false
	for _, value := range devices {
		if value.RustDeskID == input.TargetID {
			device = value
			found = true
			break
		}
	}
	if !found {
		writeError(response, 404, "device not found")
		return
	}
	rules, err := s.repository.ListACLRules(request.Context())
	if err != nil {
		writeError(response, 500, "ACL unavailable")
		return
	}
	memberships, err := s.repository.ListUserGroupMemberships(request.Context())
	if err != nil {
		writeError(response, 500, "memberships unavailable")
		return
	}
	userGroups := map[string]bool{}
	userGroupIDs := make([]string, 0)
	for _, membership := range memberships {
		if membership.Active && membership.UserID == user.ID {
			userGroups[membership.GroupID] = true
			userGroupIDs = append(userGroupIDs, membership.GroupID)
		}
	}
	matched := make([]string, 0)
	trace := make([]map[string]any, 0, len(rules))
	enabledCount := 0
	winningPriority := int(^uint(0) >> 1)
	winningEffect := ""
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		enabledCount++
		subject := rule.SubjectID == "" || (rule.SubjectType == "user" && rule.SubjectID == user.ID) || (rule.SubjectType == "user_group" && userGroups[rule.SubjectID])
		target := rule.TargetID == "" || (rule.TargetType == "device" && rule.TargetID == device.RustDeskID) || (rule.TargetType == "device_group" && device.GroupID != "" && rule.TargetID == device.GroupID)
		permissionMatched := false
		for _, permission := range rule.Permissions {
			if permission == input.Permission {
				permissionMatched = true
				break
			}
		}
		matches := subject && target && permissionMatched
		trace = append(trace, map[string]any{"rule_id": rule.ID, "name": rule.Name, "priority": rule.Priority, "effect": rule.Effect, "subject_matched": subject, "target_matched": target, "permission_matched": permissionMatched, "matched": matches})
		if matches {
			matched = append(matched, rule.ID)
			if rule.Priority < winningPriority || (rule.Priority == winningPriority && rule.Effect == "deny") {
				winningPriority, winningEffect = rule.Priority, rule.Effect
			}
		}
	}
	allowed := user.Enabled && user.ApprovalStatus == domain.ApprovalApproved && (user.Role == domain.RoleAdmin || enabledCount == 0 || winningEffect == "allow")
	reason := "matched_acl_rule"
	if !user.Enabled {
		reason = "user_disabled"
	} else if user.ApprovalStatus != domain.ApprovalApproved {
		reason = "user_not_approved"
	} else if user.Role == domain.RoleAdmin {
		reason = "administrator_bypass"
	} else if enabledCount == 0 {
		reason = "no_acl_rules_configured"
	} else if winningEffect == "deny" {
		reason = "explicit_deny"
	} else if !allowed {
		reason = "no_matching_acl_rule"
	}
	writeJSON(response, 200, map[string]any{"allowed": allowed, "reason": reason, "winning_effect": winningEffect, "winning_priority": winningPriority, "matched_rule_ids": matched, "matched_rules": trace, "enabled_rule_count": enabledCount, "user_id": user.ID, "user_group_ids": userGroupIDs, "target_id": device.RustDeskID, "target_group_id": device.GroupID, "permission": input.Permission})
}
func (s *Server) createACLRule(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var v domain.ACLRule
	if decodeJSON(request, &v, 32<<10) != nil {
		writeError(response, 400, "invalid ACL rule")
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	v.Effect = normalizeACLEffect(v.Effect)
	if v.Priority == 0 {
		v.Priority = 100
	}
	if err := s.validateACLRule(request.Context(), v); err != nil {
		writeError(response, 400, err.Error())
		return
	}
	now := time.Now().UTC()
	v.ID = uuid.NewString()
	v.Enabled = true
	v.CreatedAt = now
	v.UpdatedAt = now
	if s.repository.CreateACLRule(request.Context(), v) != nil {
		writeError(response, 500, "failed to create ACL rule")
		return
	}
	s.hub.Publish(events.ACLUpdated, v)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "acl_update", ActorUserID: principal.User.ID, Result: "success"})
	writeJSON(response, 201, v)
}
func (s *Server) updateACLRule(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var value domain.ACLRule
	if decodeJSON(request, &value, 32<<10) != nil {
		writeError(response, 400, "invalid ACL rule")
		return
	}
	value.Name = strings.TrimSpace(value.Name)
	value.Effect = normalizeACLEffect(value.Effect)
	if err := s.validateACLRule(request.Context(), value); err != nil {
		writeError(response, 400, err.Error())
		return
	}
	value.ID = request.PathValue("ruleID")
	value.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateACLRule(request.Context(), value); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "ACL rule not found")
		return
	} else if err != nil {
		writeError(response, 500, "failed to update ACL rule")
		return
	}
	s.hub.Publish(events.ACLUpdated, value)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "acl_update", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"rule_id": value.ID}})
	writeJSON(response, 200, value)
}
func (s *Server) deleteACLRule(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	id := request.PathValue("ruleID")
	if err := s.repository.DeleteACLRule(request.Context(), id); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "ACL rule not found")
		return
	} else if err != nil {
		writeError(response, 500, "failed to delete ACL rule")
		return
	}
	s.hub.Publish(events.ACLUpdated, domain.ACLRule{ID: id, Deleted: true})
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "acl_delete", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"rule_id": id}})
	response.WriteHeader(http.StatusNoContent)
}
func validACLRule(value domain.ACLRule) bool {
	allowed := map[string]bool{"remote_control": true, "file_transfer": true, "clipboard": true, "tcp_tunnel": true, "terminal": true, "audio": true, "camera": true, "view": true, "manage": true}
	if len(strings.TrimSpace(value.Name)) < 2 || len(value.Name) > 128 || len(value.Permissions) == 0 || value.Priority < 1 || value.Priority > 10000 || (value.Effect != "allow" && value.Effect != "deny") ||
		(value.SubjectType != "user" && value.SubjectType != "user_group") || (value.TargetType != "device" && value.TargetType != "device_group") {
		return false
	}
	for _, permission := range value.Permissions {
		if !allowed[permission] {
			return false
		}
	}
	return true
}

func normalizeACLEffect(value string) string {
	if strings.TrimSpace(value) == "" {
		return "allow"
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func (s *Server) validateACLRule(ctx context.Context, value domain.ACLRule) error {
	if !validACLRule(value) {
		return errors.New("invalid ACL rule")
	}
	if value.SubjectID != "" {
		if value.SubjectType == "user" {
			if _, err := s.repository.FindUserByID(ctx, value.SubjectID); err != nil {
				return errors.New("ACL subject not found")
			}
		} else {
			group, err := s.repository.FindGroupByID(ctx, value.SubjectID)
			if err != nil || group.Kind != domain.GroupKindUser {
				return errors.New("user group not found")
			}
		}
	}
	if value.TargetID != "" {
		if value.TargetType == "device" {
			devices, err := s.repository.ListDevices(ctx)
			if err != nil {
				return errors.New("devices unavailable")
			}
			found := false
			for _, device := range devices {
				if device.RustDeskID == value.TargetID {
					found = true
					break
				}
			}
			if !found {
				return errors.New("target device not found")
			}
		} else {
			group, err := s.repository.FindGroupByID(ctx, value.TargetID)
			if err != nil || group.Kind != domain.GroupKindDevice {
				return errors.New("device group not found")
			}
		}
	}
	return nil
}
func (s *Server) listStrategies(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListStrategies(request.Context())
	if err != nil {
		writeError(response, 500, "failed to list strategies")
		return
	}
	visible := make([]domain.Strategy, 0, len(values))
	for _, value := range values {
		if !value.Deleted {
			visible = append(visible, value)
		}
	}
	writeJSON(response, 200, visible)
}
func (s *Server) strategySchema(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, 200, strategyservice.Definitions())
}
func (s *Server) evaluateStrategy(response http.ResponseWriter, request *http.Request) {
	var input struct {
		DeviceID string `json:"device_id"`
	}
	if decodeJSON(request, &input, 16<<10) != nil || input.DeviceID == "" {
		writeError(response, 400, "invalid request")
		return
	}
	devices, err := s.repository.ListDevices(request.Context())
	if err != nil {
		writeError(response, 500, "devices unavailable")
		return
	}
	var device domain.Device
	found := false
	for _, value := range devices {
		if value.RustDeskID == input.DeviceID {
			device = value
			found = true
			break
		}
	}
	if !found {
		writeError(response, 404, "device not found")
		return
	}
	result, err := s.strategies.EffectiveForDevice(request.Context(), device)
	if err != nil {
		writeError(response, 500, "strategy evaluation unavailable")
		return
	}
	writeJSON(response, 200, result)
}
func (s *Server) createStrategy(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var v domain.Strategy
	if decodeJSON(request, &v, 64<<10) != nil {
		writeError(response, 400, "invalid strategy")
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	if v.Priority == 0 {
		v.Priority = 100
	}
	if err := s.validateStrategy(request.Context(), v); err != nil {
		writeError(response, 400, err.Error())
		return
	}
	now := time.Now().UTC()
	v.ID = uuid.NewString()
	v.Enabled = true
	v.CreatedAt = now
	v.UpdatedAt = now
	if s.repository.CreateStrategy(request.Context(), v) != nil {
		writeError(response, 500, "failed to create strategy")
		return
	}
	s.hub.Publish(events.StrategyUpdated, v)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "strategy_update", ActorUserID: principal.User.ID, Result: "success"})
	writeJSON(response, 201, v)
}
func (s *Server) updateStrategy(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var value domain.Strategy
	if decodeJSON(request, &value, 64<<10) != nil {
		writeError(response, 400, "invalid strategy")
		return
	}
	if err := s.validateStrategy(request.Context(), value); err != nil {
		writeError(response, 400, err.Error())
		return
	}
	value.ID = request.PathValue("strategyID")
	value.Name = strings.TrimSpace(value.Name)
	value.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateStrategy(request.Context(), value); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "strategy not found")
		return
	} else if err != nil {
		writeError(response, 500, "failed to update strategy")
		return
	}
	s.hub.Publish(events.StrategyUpdated, value)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "strategy_update", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"strategy_id": value.ID}})
	writeJSON(response, 200, value)
}
func (s *Server) deleteStrategy(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	id := request.PathValue("strategyID")
	if err := s.repository.DeleteStrategy(request.Context(), id); errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "strategy not found")
		return
	} else if err != nil {
		writeError(response, 500, "failed to delete strategy")
		return
	}
	s.hub.Publish(events.StrategyUpdated, domain.Strategy{ID: id, Deleted: true})
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "strategy_delete", ActorUserID: principal.User.ID, Result: "success", Metadata: map[string]any{"strategy_id": id}})
	response.WriteHeader(http.StatusNoContent)
}
func validStrategy(value domain.Strategy) bool {
	allowedScopes := map[string]bool{"global": true, "user": true, "user_group": true, "device": true, "device_group": true}
	if len(strings.TrimSpace(value.Name)) < 2 || len(value.Name) > 128 || !allowedScopes[value.ScopeType] || len(value.Settings) == 0 || len(value.Settings) > 64 || value.Priority < 1 || value.Priority > 100000 {
		return false
	}
	if value.ScopeType == "global" && value.ScopeID != "" || value.ScopeType != "global" && (value.ScopeID == "" || len(value.ScopeID) > 128) {
		return false
	}
	return strategyservice.ValidateSettings(value.Settings) == nil
}

func (s *Server) validateStrategy(ctx context.Context, value domain.Strategy) error {
	if !validStrategy(value) {
		return errors.New("invalid strategy")
	}
	if value.ScopeType == "global" {
		return nil
	}
	switch value.ScopeType {
	case "user":
		if _, err := s.repository.FindUserByID(ctx, value.ScopeID); err != nil {
			return errors.New("strategy user not found")
		}
	case "user_group", "device_group":
		group, err := s.repository.FindGroupByID(ctx, value.ScopeID)
		expected := domain.GroupKindUser
		if value.ScopeType == "device_group" {
			expected = domain.GroupKindDevice
		}
		if err != nil || group.Kind != expected {
			return errors.New("strategy group not found")
		}
	case "device":
		devices, err := s.repository.ListDevices(ctx)
		if err != nil {
			return errors.New("devices unavailable")
		}
		for _, device := range devices {
			if device.RustDeskID == value.ScopeID {
				return nil
			}
		}
		return errors.New("strategy device not found")
	}
	return nil
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok", "service": "art-api"})
}

func (s *Server) loginOptions(response http.ResponseWriter, _ *http.Request) {
	if s.oidc == nil {
		writeJSON(response, http.StatusOK, []any{})
		return
	}
	writeJSON(response, http.StatusOK, []map[string]string{{"name": s.oidc.Name()}})
}

type deviceInfo struct {
	OS   string `json:"os"`
	Type string `json:"type"`
	Name string `json:"name"`
}

type loginRequest struct {
	Username         string     `json:"username"`
	Password         string     `json:"password"`
	ID               string     `json:"id"`
	UUID             string     `json:"uuid"`
	Type             string     `json:"type"`
	AutoLogin        bool       `json:"autoLogin"`
	DeviceInfo       deviceInfo `json:"deviceInfo"`
	VerificationCode string     `json:"verification_code"`
	TFACode          string     `json:"tfaCode"`
	Secret           string     `json:"secret"`
}

func (s *Server) login(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	ip := clientIP(request)
	if allowed, retryAfter := s.loginLimiter.Allowed(ip, now); !allowed {
		response.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
		writeError(response, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var input loginRequest
	if err := decodeJSON(request, &input, 16<<10); err != nil {
		s.loginLimiter.Failure(ip, now)
		writeError(response, http.StatusBadRequest, "invalid request")
		return
	}
	input.Username = strings.TrimSpace(strings.ToLower(input.Username))
	if input.Type != "tfa_code" {
		if allowed, retryAfter := s.accountLoginAllowed(request.Context(), input.Username, now); !allowed {
			response.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
			_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "login_failed", IP: ip, Result: "denied", Reason: "account_locked", Metadata: map[string]any{"username": input.Username}})
			writeError(response, http.StatusTooManyRequests, "account temporarily locked")
			return
		}
	}
	var user domain.User
	var err error
	recoveryUsed := false
	if input.Type == "tfa_code" {
		if len(input.Secret) < 16 || len(input.Secret) > 128 || len(input.TFACode) < 6 || len(input.TFACode) > 32 {
			s.loginLimiter.Failure(ip, now)
			writeError(response, http.StatusBadRequest, "invalid request")
			return
		}
		challenge, challengeErr := s.repository.FindAuthChallenge(request.Context(), input.Secret, now)
		if challengeErr != nil {
			s.loginLimiter.Failure(ip, now)
			writeError(response, http.StatusUnauthorized, "two-factor challenge expired")
			return
		}
		user, err = s.repository.FindUserByID(request.Context(), challenge.UserID)
		if err != nil || !user.Enabled || user.TokenVersion != challenge.TokenVersion {
			writeError(response, http.StatusUnauthorized, "two-factor challenge expired")
			return
		}
		var valid bool
		valid, recoveryUsed, err = s.mfa.Verify(request.Context(), user, input.TFACode)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "two-factor verification failed")
			return
		}
		if !valid {
			s.loginLimiter.Failure(ip, now)
			s.accountLoginFailure(request.Context(), user.Username, now)
			_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "login_failed", ActorUserID: user.ID, IP: ip, Result: "denied", Reason: "invalid_mfa"})
			writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "invalid two-factor code", "type": "tfa_check", "tfa_type": "totp", "secret": input.Secret})
			return
		}
		if _, err = s.repository.ConsumeAuthChallenge(request.Context(), input.Secret, now); err != nil {
			writeError(response, http.StatusUnauthorized, "two-factor challenge already used")
			return
		}
		input.ID, input.UUID = challenge.RustDeskID, challenge.ClientUUID
		input.DeviceInfo = deviceInfo{OS: challenge.Platform, Type: challenge.ClientType, Name: challenge.DeviceName}
		input.Username = user.Username
	} else {
		if len(input.Username) < 2 || len(input.Username) > 128 || len(input.Password) < 1 || len(input.Password) > 1024 {
			s.loginLimiter.Failure(ip, now)
			writeError(response, http.StatusBadRequest, "invalid request")
			return
		}
		user, err = s.auth.VerifyCredentials(request.Context(), input.Username, input.Password)
	}
	if err != nil {
		s.loginLimiter.Failure(ip, now)
		s.accountLoginFailure(request.Context(), input.Username, now)
		reason := "invalid_credentials"
		status := http.StatusUnauthorized
		message := "invalid username or password"
		if errors.Is(err, auth.ErrUserDisabled) {
			reason, status, message = "user_disabled", http.StatusForbidden, "user disabled"
		} else if errors.Is(err, auth.ErrApprovalRejected) {
			reason, status, message = "registration_rejected", http.StatusForbidden, "registration rejected"
		}
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "login_failed", IP: ip,
			Result: "denied", Reason: reason, Metadata: map[string]any{"username": input.Username}})
		writeError(response, status, message)
		return
	}
	if input.Type != "tfa_code" && !user.TOTPEnabled && s.mfa.Required(user) {
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "login_failed", ActorUserID: user.ID, IP: ip, Result: "denied", Reason: "mfa_enrollment_required"})
		writeJSON(response, http.StatusForbidden, map[string]any{"error": "two-factor enrollment required", "mfa_enrollment_required": true})
		return
	}
	if input.Type != "tfa_code" && user.TOTPEnabled {
		valid, used, verifyErr := s.mfa.Verify(request.Context(), user, input.VerificationCode)
		if verifyErr != nil {
			writeError(response, http.StatusInternalServerError, "two-factor verification failed")
			return
		}
		recoveryUsed = used
		if !valid {
			if input.VerificationCode != "" {
				s.loginLimiter.Failure(ip, now)
				s.accountLoginFailure(request.Context(), user.Username, now)
			}
			if input.Type != "web" {
				challenge := domain.AuthChallenge{ID: uuid.NewString(), UserID: user.ID, TokenVersion: user.TokenVersion, CreatedAt: now, ExpiresAt: now.Add(3 * time.Minute),
					IP: ip, UserAgent: request.UserAgent(), ClientDeviceID: firstNonEmpty(input.UUID, input.ID), RustDeskID: input.ID, ClientUUID: input.UUID,
					Platform: input.DeviceInfo.OS, ClientType: input.DeviceInfo.Type, DeviceName: input.DeviceInfo.Name}
				if err := s.repository.CreateAuthChallenge(request.Context(), challenge); err != nil {
					writeError(response, http.StatusInternalServerError, "two-factor challenge failed")
					return
				}
				_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "mfa_challenge_created", ActorUserID: user.ID, IP: ip, Result: "success", Metadata: map[string]any{"expires_in_seconds": 180}})
				writeJSON(response, http.StatusOK, map[string]any{"access_token": "", "type": "tfa_check", "tfa_type": "totp", "secret": challenge.ID, "user": clientUser(user)})
				return
			}
			writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "two-factor authentication required", "requires_2fa": true})
			return
		}
	}
	deviceID := firstNonEmpty(input.UUID, input.ID)
	loginInput := auth.LoginInput{Username: input.Username, Password: input.Password, IP: ip, UserAgent: request.UserAgent(), ClientDeviceID: deviceID}
	result, err := s.auth.CompleteLogin(request.Context(), user, loginInput)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "login failed")
		return
	}
	s.loginLimiter.Success(ip)
	s.accountLoginSuccess(request.Context(), user.Username)
	if input.ID != "" && input.Type != "web" {
		device, _, identityMismatch, inventoryErr := s.inventoryDevice(request, input.ID, input.UUID)
		if inventoryErr == nil && !identityMismatch {
			device.RustDeskID, device.ClientUUID = input.ID, input.UUID
			if device.Hostname == "" {
				device.Hostname = input.DeviceInfo.Name
			}
			if device.Platform == "" {
				device.Platform = input.DeviceInfo.OS
			}
			device.Online, device.LastSeen, device.LastSeenIP, device.OwnerUserID = true, now, ip, result.User.ID
			if device.CreatedAt.IsZero() {
				device.CreatedAt = now
			}
			_ = s.repository.UpsertDevice(request.Context(), device)
		} else if identityMismatch {
			_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_identity_mismatch", ActorUserID: result.User.ID,
				ActorSessionID: result.Session.ID, TargetRustDeskID: input.ID, IP: ip, Result: "denied", Reason: "uuid_mismatch"})
		}
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "login_success", ActorUserID: result.User.ID,
		ActorSessionID: result.Session.ID, ControllerDevice: deviceID, IP: ip, Result: "allowed",
		Metadata: map[string]any{"client_type": input.DeviceInfo.Type, "platform": input.DeviceInfo.OS, "recovery_code_used": recoveryUsed}})
	writeJSON(response, http.StatusOK, map[string]any{
		"access_token": result.AccessToken,
		"type":         "access_token",
		"expires_at":   result.Claims.ExpiresAt.Time.UTC().Format(time.RFC3339),
		"user":         clientUser(result.User),
	})
}

func (s *Server) logout(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if err := s.auth.RevokeSession(request.Context(), principal.Session.ID); err != nil {
		writeError(response, http.StatusInternalServerError, "logout failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "logout", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, IP: clientIP(request), Result: "success"})
	writeJSON(response, http.StatusOK, nil)
}

func (s *Server) logoutAll(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if err := s.auth.RevokeAll(request.Context(), principal.User.ID); err != nil {
		writeError(response, http.StatusInternalServerError, "logout failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "session_revoke_all",
		ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	writeJSON(response, http.StatusOK, nil)
}

func (s *Server) me(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	writeJSON(response, http.StatusOK, clientUser(principal.User))
}

func (s *Server) revokeSession(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	sessionID := request.PathValue("sessionID")
	if sessionID != principal.Session.ID && !hasPermission(principal.User, domain.PermissionSessionsRevoke) {
		writeError(response, http.StatusForbidden, "forbidden")
		return
	}
	if err := s.auth.RevokeSession(request.Context(), sessionID); err != nil {
		writeError(response, http.StatusInternalServerError, "session revoke failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "session_revoke",
		ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success",
		Metadata: map[string]any{"revoked_session_id": sessionID}})
	writeJSON(response, http.StatusOK, nil)
}

func (s *Server) disableUser(response http.ResponseWriter, request *http.Request) {
	s.setUserEnabled(response, request, false)
}

func (s *Server) enableUser(response http.ResponseWriter, request *http.Request) {
	s.setUserEnabled(response, request, true)
}

func (s *Server) setUserEnabled(response http.ResponseWriter, request *http.Request, enabled bool) {
	principal, _ := principalFrom(request.Context())
	user, err := s.auth.SetUserEnabled(request.Context(), request.PathValue("userID"), enabled)
	if err != nil {
		writeError(response, http.StatusNotFound, "user not found")
		return
	}
	eventType := "user_enable"
	if !enabled {
		eventType = "user_disable"
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: eventType, ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"user_id": user.ID}})
	writeJSON(response, http.StatusOK, clientUser(user))
}

func (s *Server) forceRelogin(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	user, err := s.auth.ForceRelogin(request.Context(), request.PathValue("userID"))
	if err != nil {
		writeError(response, http.StatusNotFound, "user not found")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "force_relogin", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"user_id": user.ID}})
	writeJSON(response, http.StatusOK, clientUser(user))
}

func (s *Server) listUsers(response http.ResponseWriter, request *http.Request) {
	users, err := s.repository.ListUsers(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "users unavailable")
		return
	}
	memberships, err := s.repository.ListUserGroupMemberships(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "users unavailable")
		return
	}
	groupIDs := make(map[string][]string)
	for _, membership := range memberships {
		groupIDs[membership.UserID] = append(groupIDs[membership.UserID], membership.GroupID)
	}
	output := make([]map[string]any, 0, len(users))
	for _, user := range users {
		value := clientUser(user)
		value["group_ids"] = groupIDs[user.ID]
		output = append(output, value)
	}
	writeJSON(response, http.StatusOK, output)
}

type createUserRequest struct {
	Username    string      `json:"username"`
	Email       string      `json:"email"`
	Password    string      `json:"password"`
	DisplayName string      `json:"display_name"`
	Phone       string      `json:"phone"`
	GroupIDs    []string    `json:"group_ids"`
	Role        domain.Role `json:"role"`
	Enabled     *bool       `json:"enabled"`
}

type updateUserRequest struct {
	Username    string      `json:"username"`
	Email       string      `json:"email"`
	Phone       string      `json:"phone"`
	Password    string      `json:"password"`
	DisplayName string      `json:"display_name"`
	Role        domain.Role `json:"role"`
	Enabled     *bool       `json:"enabled"`
	GroupIDs    []string    `json:"group_ids"`
}

func (s *Server) registrationOptions(response http.ResponseWriter, _ *http.Request) {
	configured := s.runtimeConfiguration()
	writeJSON(response, http.StatusOK, map[string]any{"enabled": configured.RegistrationEnabled, "approval_required": !configured.RegistrationAutoApprove})
}

func (s *Server) register(response http.ResponseWriter, request *http.Request) {
	if !s.registrationIsEnabled() {
		writeError(response, http.StatusNotFound, "registration is disabled")
		return
	}
	now, ip := time.Now().UTC(), clientIP(request)
	if allowed, retryAfter := s.registrationLimiter.Allowed(ip, now); !allowed {
		response.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Seconds()))))
		writeError(response, http.StatusTooManyRequests, "too many registration attempts")
		return
	}
	s.registrationLimiter.Failure(ip, now)
	var input createUserRequest
	if err := decodeJSON(request, &input, 16<<10); err != nil {
		writeError(response, 400, "invalid request")
		return
	}
	input.Username, input.Email, input.DisplayName = strings.ToLower(strings.TrimSpace(input.Username)), strings.TrimSpace(input.Email), strings.TrimSpace(input.DisplayName)
	if !validUsername(input.Username) || len(input.Password) < 1 || len(input.Password) > 1024 || len(input.DisplayName) > 256 || !validEmail(input.Email) {
		writeError(response, 400, "invalid registration data")
		return
	}
	autoApprove := s.runtimeConfiguration().RegistrationAutoApprove
	user, err := s.auth.Register(request.Context(), auth.CreateUserInput{Username: input.Username, Email: input.Email, Password: input.Password, DisplayName: input.DisplayName}, autoApprove)
	if err != nil {
		writeError(response, http.StatusConflict, "account could not be registered")
		return
	}
	result, message := domain.ApprovalPending, "Регистрация завершена. Учётная запись ожидает подтверждения администратора."
	if autoApprove {
		result, message = domain.ApprovalApproved, "Регистрация завершена. Учётная запись авторизована и готова к работе."
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "user_registration", ActorUserID: user.ID, IP: ip, Result: string(result), Metadata: map[string]any{"username": user.Username, "automatic_approval": autoApprove}})
	writeJSON(response, http.StatusAccepted, map[string]any{"status": result, "message": message})
}

func (s *Server) createUser(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input createUserRequest
	if err := decodeJSON(request, &input, 16<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid request")
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Email = strings.TrimSpace(input.Email)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Phone = strings.TrimSpace(input.Phone)
	if !validUsername(input.Username) || len(input.Password) < 1 || len(input.Password) > 1024 ||
		len(input.DisplayName) > 256 || len(input.Phone) > 64 || len(input.GroupIDs) > 128 || !validEmail(input.Email) ||
		!s.canAssignRole(request, principal.User, input.Role) {
		writeError(response, http.StatusBadRequest, "invalid user")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	user, err := s.auth.CreateLocalUser(request.Context(), auth.CreateUserInput{Username: input.Username,
		Email: input.Email, Password: input.Password, DisplayName: input.DisplayName, Role: input.Role, Enabled: enabled})
	if err != nil {
		writeError(response, http.StatusConflict, "user could not be created")
		return
	}
	if input.Phone != "" || len(input.GroupIDs) != 0 {
		user, err = s.auth.UpdateUser(request.Context(), user.ID, domain.UserUpdate{Username: user.Username, Email: user.Email, Phone: input.Phone, DisplayName: user.DisplayName, Role: user.Role, Enabled: user.Enabled, GroupIDs: input.GroupIDs}, "")
		if err != nil {
			writeError(response, http.StatusConflict, "user groups could not be assigned")
			return
		}
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "user_create", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"user_id": user.ID}})
	writeJSON(response, http.StatusCreated, clientUser(user))
}

func (s *Server) updateUser(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input updateUserRequest
	if decodeJSON(request, &input, 20<<10) != nil {
		writeError(response, 400, "invalid user update")
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Email = strings.TrimSpace(input.Email)
	input.Phone = strings.TrimSpace(input.Phone)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if !validUsername(input.Username) || !validEmail(input.Email) || len(input.Phone) > 64 || len(input.DisplayName) > 256 || len(input.Password) > 1024 || input.Enabled == nil || !s.canAssignRole(request, principal.User, input.Role) || len(input.GroupIDs) > 128 {
		writeError(response, 400, "invalid user update")
		return
	}
	user, err := s.auth.UpdateUser(request.Context(), request.PathValue("userID"), domain.UserUpdate{Username: input.Username, Email: input.Email, Phone: input.Phone, DisplayName: input.DisplayName, Role: input.Role, Enabled: *input.Enabled, GroupIDs: input.GroupIDs}, input.Password)
	if errors.Is(err, domain.ErrLastAdmin) {
		writeError(response, 409, "last administrator cannot be changed")
		return
	}
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "user or group not found")
		return
	}
	if err != nil {
		writeError(response, 409, "user could not be updated")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "user_update", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"user_id": user.ID}})
	writeJSON(response, 200, clientUser(user))
}

func (s *Server) validRole(request *http.Request, role domain.Role) bool {
	if role == domain.RoleAdmin || role == domain.RoleUser {
		return true
	}
	_, err := s.repository.FindRoleByID(request.Context(), role)
	return err == nil
}

func (s *Server) canAssignRole(request *http.Request, actor domain.User, role domain.Role) bool {
	if !s.validRole(request, role) {
		return false
	}
	if actor.Role == domain.RoleAdmin {
		return true
	}
	if role == domain.RoleAdmin {
		return false
	}
	definition, err := s.repository.FindRoleByID(request.Context(), role)
	return err == nil && canGrant(actor, definition.Permissions)
}

func (s *Server) deleteUser(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	userID := request.PathValue("userID")
	if userID == principal.User.ID {
		writeError(response, 409, "current administrator cannot be deleted")
		return
	}
	if err := s.auth.DeleteUser(request.Context(), userID); errors.Is(err, domain.ErrLastAdmin) {
		writeError(response, 409, "last administrator cannot be deleted")
		return
	} else if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "user not found")
		return
	} else if err != nil {
		writeError(response, 409, "user could not be deleted")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "user_delete", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"user_id": userID}})
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) approveUser(response http.ResponseWriter, request *http.Request) {
	s.setUserApproval(response, request, domain.ApprovalApproved)
}
func (s *Server) rejectUser(response http.ResponseWriter, request *http.Request) {
	s.setUserApproval(response, request, domain.ApprovalRejected)
}
func (s *Server) setUserApproval(response http.ResponseWriter, request *http.Request, status domain.ApprovalStatus) {
	principal, _ := principalFrom(request.Context())
	user, err := s.auth.SetUserApproval(request.Context(), request.PathValue("userID"), status)
	if err != nil {
		writeError(response, 404, "user not found")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "user_approval_update", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: string(status), Metadata: map[string]any{"user_id": user.ID}})
	writeJSON(response, 200, clientUser(user))
}

func (s *Server) setUserPassword(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(request, &input, 4<<10); err != nil || len(input.Password) < 1 || len(input.Password) > 1024 {
		writeError(response, http.StatusBadRequest, "invalid password")
		return
	}
	user, err := s.auth.SetPassword(request.Context(), request.PathValue("userID"), input.Password)
	if err != nil {
		writeError(response, http.StatusNotFound, "user not found")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "user_password_reset", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"user_id": user.ID}})
	writeJSON(response, http.StatusOK, nil)
}

func (s *Server) listDevices(response http.ResponseWriter, request *http.Request) {
	devices, err := s.repository.ListDevices(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "devices unavailable")
		return
	}
	includeArchived := request.URL.Query().Get("archived") == "true"
	filtered := make([]domain.Device, 0, len(devices))
	for _, device := range devices {
		if (device.ArchivedAt != nil) == includeArchived {
			filtered = append(filtered, device)
		}
	}
	writeJSON(response, http.StatusOK, filtered)
}

func (s *Server) updateDevice(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input struct {
		Alias   string   `json:"alias"`
		GroupID string   `json:"group_id"`
		Tags    []string `json:"tags"`
	}
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, 400, "invalid device update")
		return
	}
	input.Alias = strings.TrimSpace(input.Alias)
	input.GroupID = strings.TrimSpace(input.GroupID)
	if len(input.Alias) > 128 || len(input.GroupID) > 128 || len(input.Tags) > 32 {
		writeError(response, 400, "invalid device update")
		return
	}
	for index, tag := range input.Tags {
		input.Tags[index] = strings.TrimSpace(tag)
		if input.Tags[index] == "" || len(input.Tags[index]) > 64 {
			writeError(response, 400, "invalid tag")
			return
		}
	}
	device, err := s.repository.UpdateDeviceManagement(request.Context(), request.PathValue("rustdeskID"), input.Alias, input.GroupID, input.Tags)
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, 404, "device not found")
		return
	}
	if err != nil {
		writeError(response, 500, "device update failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_update", ActorUserID: principal.User.ID, TargetRustDeskID: device.RustDeskID, Result: "success"})
	writeJSON(response, 200, device)
}

func (s *Server) bulkUpdateDevices(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input struct {
		IDs        []string `json:"ids"`
		GroupID    *string  `json:"group_id"`
		AddTags    []string `json:"add_tags"`
		RemoveTags []string `json:"remove_tags"`
	}
	if decodeJSON(request, &input, 64<<10) != nil || len(input.IDs) < 1 || len(input.IDs) > 500 || len(input.AddTags) > 32 || len(input.RemoveTags) > 32 {
		writeError(response, 400, "invalid bulk device update")
		return
	}
	if input.GroupID != nil {
		value := strings.TrimSpace(*input.GroupID)
		input.GroupID = &value
	}
	seen := map[string]bool{}
	for index, id := range input.IDs {
		id = strings.TrimSpace(id)
		if len(id) < 3 || len(id) > 64 || seen[id] {
			writeError(response, 400, "invalid device id")
			return
		}
		seen[id] = true
		input.IDs[index] = id
	}
	for index, tag := range input.AddTags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > 64 {
			writeError(response, 400, "invalid tag")
			return
		}
		input.AddTags[index] = tag
	}
	for index, tag := range input.RemoveTags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > 64 {
			writeError(response, 400, "invalid tag")
			return
		}
		input.RemoveTags[index] = tag
	}
	if input.GroupID != nil && *input.GroupID != "" {
		group, err := s.repository.FindGroupByID(request.Context(), *input.GroupID)
		if err != nil || group.Kind != domain.GroupKindDevice {
			writeError(response, 400, "invalid device group")
			return
		}
	}
	if err := s.repository.BulkUpdateDevices(request.Context(), input.IDs, input.GroupID, input.AddTags, input.RemoveTags); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(response, 404, "device not found")
		} else {
			writeError(response, 500, "bulk device update failed")
		}
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_bulk_update", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"device_ids": input.IDs, "group_id": input.GroupID, "add_tags": input.AddTags, "remove_tags": input.RemoveTags}})
	devices, _ := s.repository.ListDevices(request.Context())
	writeJSON(response, 200, devices)
}

func (s *Server) archiveDevice(response http.ResponseWriter, request *http.Request) {
	s.setDeviceArchived(response, request, true)
}

func (s *Server) restoreDevice(response http.ResponseWriter, request *http.Request) {
	s.setDeviceArchived(response, request, false)
}

func (s *Server) setDeviceArchived(response http.ResponseWriter, request *http.Request, archived bool) {
	principal, _ := principalFrom(request.Context())
	device, err := s.repository.SetDeviceArchived(request.Context(), request.PathValue("rustdeskID"), archived, time.Now().UTC())
	if errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusNotFound, "device not found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "device lifecycle update failed")
		return
	}
	eventType := "device_restore"
	if archived {
		eventType = "device_archive"
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: eventType, ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, TargetRustDeskID: device.RustDeskID, Result: "success"})
	writeJSON(response, http.StatusOK, device)
}

func (s *Server) deleteArchivedDevice(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	id := request.PathValue("rustdeskID")
	if err := s.repository.DeleteArchivedDevice(request.Context(), id); errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusConflict, "only archived devices can be permanently deleted")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "device deletion failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "device_delete", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, TargetRustDeskID: id, Result: "success"})
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) backupSQLite(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if envValue("ART_DB_DRIVER", "sqlite") != "sqlite" {
		writeError(response, 409, "online backup is available only for SQLite")
		return
	}
	directory, err := os.MkdirTemp("", "art-backup-")
	if err != nil {
		writeError(response, 500, "backup unavailable")
		return
	}
	defer os.RemoveAll(directory)
	name := "rustdesk-server-routeros-" + time.Now().UTC().Format("20060102-150405") + ".db"
	path := filepath.Join(directory, name)
	if err = s.repository.Backup(request.Context(), path); err != nil {
		writeError(response, 500, "backup failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_backup", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success"})
	response.Header().Set("Content-Type", "application/vnd.sqlite3")
	response.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(response, request, path)
}

func (s *Server) inspectSQLiteBackup(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	if envValue("ART_DB_DRIVER", "sqlite") != "sqlite" {
		writeError(response, http.StatusConflict, "SQLite backup inspection is unavailable for this database driver")
		return
	}
	const maximumBackupSize = int64(512 << 20)
	if request.ContentLength > maximumBackupSize {
		writeError(response, http.StatusRequestEntityTooLarge, "backup is too large")
		return
	}
	directory, err := os.MkdirTemp("", "rustdesk-backup-inspection-")
	if err != nil {
		writeError(response, http.StatusInternalServerError, "backup inspection unavailable")
		return
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "candidate.db")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "backup inspection unavailable")
		return
	}
	written, copyErr := io.Copy(file, io.LimitReader(request.Body, maximumBackupSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		writeError(response, http.StatusBadRequest, "backup upload failed")
		return
	}
	if written == 0 || written > maximumBackupSize {
		writeError(response, http.StatusRequestEntityTooLarge, "invalid backup size")
		return
	}
	inspection, err := s.repository.InspectBackup(request.Context(), path)
	if err != nil {
		_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_backup_inspection", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "denied", Reason: "invalid_backup"})
		writeError(response, http.StatusUnprocessableEntity, "invalid or incompatible SQLite backup")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "database_backup_inspection", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"size_bytes": inspection.SizeBytes, "users": inspection.Users, "devices": inspection.Devices}})
	writeJSON(response, http.StatusOK, inspection)
}

func (s *Server) listGroups(response http.ResponseWriter, request *http.Request) {
	groups, err := s.repository.ListGroups(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "groups unavailable")
		return
	}
	writeJSON(response, http.StatusOK, groups)
}

func (s *Server) createGroup(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	var input struct {
		Name        string           `json:"name"`
		Description string           `json:"description"`
		Kind        domain.GroupKind `json:"kind"`
	}
	if err := decodeJSON(request, &input, 16<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid request")
		return
	}
	input.Name, input.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	if len(input.Name) < 2 || len(input.Name) > 128 || len(input.Description) > 512 ||
		(input.Kind != domain.GroupKindUser && input.Kind != domain.GroupKindDevice) {
		writeError(response, http.StatusBadRequest, "invalid group")
		return
	}
	now := time.Now().UTC()
	group := domain.Group{ID: uuid.NewString(), Name: input.Name, Description: input.Description,
		Kind: input.Kind, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateGroup(request.Context(), group); err != nil {
		writeError(response, http.StatusConflict, "group could not be created")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "group_create", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"group_id": group.ID}})
	writeJSON(response, http.StatusCreated, group)
}

func (s *Server) updateGroup(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	group, err := s.repository.FindGroupByID(request.Context(), request.PathValue("groupID"))
	if err != nil {
		writeError(response, statusForStore(err), "group not found")
		return
	}
	if group.ID == domain.PendingUsersGroupID || group.ID == domain.ApprovedUsersGroupID {
		writeError(response, http.StatusConflict, "system group cannot be edited")
		return
	}
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, 400, "invalid request")
		return
	}
	group.Name, group.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	if len(group.Name) < 2 || len(group.Name) > 128 || len(group.Description) > 512 {
		writeError(response, 400, "invalid group")
		return
	}
	group.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateGroup(request.Context(), group); err != nil {
		writeError(response, statusForStore(err), "group could not be updated")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "group_update", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"group_id": group.ID}})
	writeJSON(response, 200, group)
}

func (s *Server) deleteGroup(response http.ResponseWriter, request *http.Request) {
	principal, _ := principalFrom(request.Context())
	id := request.PathValue("groupID")
	if id == domain.PendingUsersGroupID || id == domain.ApprovedUsersGroupID {
		writeError(response, 409, "system group cannot be deleted")
		return
	}
	memberships, _ := s.repository.ListUserGroupMemberships(request.Context())
	rules, _ := s.repository.ListACLRules(request.Context())
	strategies, _ := s.repository.ListStrategies(request.Context())
	devices, _ := s.repository.ListDevices(request.Context())
	if err := s.repository.DeleteGroup(request.Context(), id); err != nil {
		writeError(response, statusForStore(err), "group could not be deleted")
		return
	}
	for _, membership := range memberships {
		if membership.GroupID == id {
			membership.Active = false
			s.hub.Publish(events.UserGroupMembershipUpdated, membership)
		}
	}
	for _, rule := range rules {
		if (rule.SubjectType == "user_group" && rule.SubjectID == id) || (rule.TargetType == "device_group" && rule.TargetID == id) {
			s.hub.Publish(events.ACLUpdated, domain.ACLRule{ID: rule.ID, Deleted: true})
		}
	}
	for _, strategy := range strategies {
		if (strategy.ScopeType == "user_group" || strategy.ScopeType == "device_group") && strategy.ScopeID == id {
			s.hub.Publish(events.StrategyUpdated, domain.Strategy{ID: strategy.ID, Deleted: true})
		}
	}
	for _, device := range devices {
		if device.GroupID == id {
			device.GroupID = ""
			s.hub.Publish(events.DeviceUpdated, device)
		}
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "group_delete", ActorUserID: principal.User.ID, ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"group_id": id}})
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) listGroupMembers(response http.ResponseWriter, request *http.Request) {
	group, err := s.repository.FindGroupByID(request.Context(), request.PathValue("groupID"))
	if errors.Is(err, domain.ErrNotFound) || group.Kind != domain.GroupKindUser {
		writeError(response, http.StatusNotFound, "user group not found")
		return
	}
	memberships, err := s.repository.ListUserGroupMemberships(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "group members unavailable")
		return
	}
	memberIDs := make(map[string]struct{})
	for _, membership := range memberships {
		if membership.GroupID == group.ID {
			memberIDs[membership.UserID] = struct{}{}
		}
	}
	users, err := s.repository.ListUsers(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "group members unavailable")
		return
	}
	result := make([]map[string]any, 0, len(memberIDs))
	for _, user := range users {
		if _, exists := memberIDs[user.ID]; exists {
			result = append(result, clientUser(user))
		}
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) addGroupMember(response http.ResponseWriter, request *http.Request) {
	s.setGroupMember(response, request, true)
}

func (s *Server) removeGroupMember(response http.ResponseWriter, request *http.Request) {
	s.setGroupMember(response, request, false)
}

func (s *Server) setGroupMember(response http.ResponseWriter, request *http.Request, active bool) {
	principal, _ := principalFrom(request.Context())
	groupID, userID := request.PathValue("groupID"), request.PathValue("userID")
	group, err := s.repository.FindGroupByID(request.Context(), groupID)
	if errors.Is(err, domain.ErrNotFound) || group.Kind != domain.GroupKindUser {
		writeError(response, http.StatusNotFound, "user group not found")
		return
	}
	if _, err := s.repository.FindUserByID(request.Context(), userID); errors.Is(err, domain.ErrNotFound) {
		writeError(response, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "membership update failed")
		return
	}
	if err := s.repository.SetUserGroupMember(request.Context(), groupID, userID, active); err != nil {
		writeError(response, http.StatusInternalServerError, "membership update failed")
		return
	}
	membership := domain.UserGroupMembership{GroupID: groupID, UserID: userID, Active: active}
	s.hub.Publish(events.UserGroupMembershipUpdated, membership)
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "group_membership_update", ActorUserID: principal.User.ID,
		ActorSessionID: principal.Session.ID, Result: "success", Metadata: map[string]any{"group_id": groupID, "user_id": userID, "active": active}})
	writeJSON(response, http.StatusOK, membership)
}

func (s *Server) listSessions(response http.ResponseWriter, request *http.Request) {
	sessions, err := s.repository.ListSessions(request.Context(), time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "sessions unavailable")
		return
	}
	writeJSON(response, http.StatusOK, sessions)
}

func (s *Server) listAudit(response http.ResponseWriter, request *http.Request) {
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	events, err := s.repository.ListAudit(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "audit unavailable")
		return
	}
	writeJSON(response, http.StatusOK, events)
}

func (s *Server) settings(response http.ResponseWriter, _ *http.Request) {
	configured := s.runtimeConfiguration()
	publicKey := ""
	keyFile := envValue("ART_SERVER_PUBLIC_KEY_FILE", envValue("ART_SERVER_KEY_FILE", "/data/secrets/id_ed25519")+".pub")
	if value, err := os.ReadFile(keyFile); err == nil {
		publicKey = strings.TrimSpace(string(value))
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"require_login":             configured.RequireLogin,
		"require_device_deployment": configured.RequireDeviceDeployment,
		"database_driver":           envValue("ART_DB_DRIVER", "sqlite"),
		"access_token_ttl":          configured.AccessTokenTTL.String(),
		"session_ttl":               configured.SessionTTL.String(),
		"login_burst":               envValue("ART_LOGIN_BURST", "5"),
		"login_window":              envValue("ART_LOGIN_WINDOW", "5m"),
		"login_lockout":             envValue("ART_LOGIN_LOCKOUT", "15m"),
		"device_online_ttl":         envValue("ART_DEVICE_ONLINE_TTL", "180s"),
		"relay_server":              envValue("ART_RELAY_SERVER", "127.0.0.1:21117"),
		"server_public_key":         publicKey,
		"mfa_mode":                  s.mfa.Mode(),
		"password_minimum_length":   configured.PasswordMinimumLength,
		"password_require_upper":    configured.PasswordRequireUpper,
		"password_require_lower":    configured.PasswordRequireLower,
		"password_require_number":   configured.PasswordRequireNumber,
		"password_require_special":  configured.PasswordRequireSpecial,
		"oidc_enabled":              s.oidc != nil,
		"oidc_provider": func() string {
			if s.oidc != nil {
				return s.oidc.Name()
			}
			return ""
		}(),
		"registration_enabled":      configured.RegistrationEnabled,
		"registration_auto_approve": configured.RegistrationAutoApprove,
		"ldap_enabled":              s.ldapEnabled,
		"ldap_auto_provision":       s.ldapAutoProvision,
		"custom_logo":               s.hasBrandingLogo(),
		"logo_url":                  "/api/branding/logo",
		"custom_global_avatar":      s.hasGlobalAvatar(),
		"global_avatar_url":         "/api/avatar/global",
	})
}

func (s *Server) infrastructure(response http.ResponseWriter, request *http.Request) {
	users, sessions, err := s.repository.ListAuthState(request.Context(), time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "infrastructure unavailable")
		return
	}
	devices, _ := s.repository.ListDevices(request.Context())
	relays, _ := s.repository.ListRelayServers(request.Context())
	onlineDevices := 0
	for _, device := range devices {
		if device.Online {
			onlineDevices++
		}
	}
	hbbsStatus, hbbsInstances, rendezvousPeers := s.runtime.service("hbbs", 20*time.Second)
	hbbrStatus, hbbrInstances, _ := s.runtime.service("hbbr", 20*time.Second)
	healthyRelays, relayConnections, relayBandwidth := 0, 0, int64(0)
	for _, relay := range relays {
		if !relay.Enabled {
			continue
		}
		if relay.Health == "healthy" {
			healthyRelays++
		}
		relayConnections += relay.Connections
		relayBandwidth += relay.Bandwidth
	}
	if hbbrStatus == "offline" && healthyRelays > 0 {
		hbbrStatus = "online"
		hbbrInstances = healthyRelays
	}
	cpuPercent, memoryBytes, memoryCgroupBytes, memoryLimit, uptimeSeconds := s.runtime.system()
	history := s.runtime.record(infrastructureSample{
		Timestamp: time.Now().UTC(), CPUPercent: cpuPercent, MemoryBytes: memoryBytes,
		OnlineDevices: onlineDevices, ActiveSessions: len(sessions),
		RelayConnections: relayConnections, RelayBandwidth: relayBandwidth,
	})
	relayAddress := envValue("ART_RELAY_SERVER", "127.0.0.1:21117")
	publicHost, _, splitErr := net.SplitHostPort(relayAddress)
	if splitErr != nil {
		publicHost = strings.TrimSpace(relayAddress)
	}
	hbbsAddress := net.JoinHostPort(publicHost, "21116")
	scheme := "http"
	if request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	apiAddress := scheme + "://" + request.Host
	writeJSON(response, http.StatusOK, map[string]any{
		"api": "online", "hbbs": hbbsStatus, "hbbr": hbbrStatus, "database": "online",
		"api_address": apiAddress, "hbbs_address": hbbsAddress, "hbbr_address": relayAddress,
		"database_driver": envValue("ART_DB_DRIVER", "sqlite"), "users": len(users),
		"active_sessions": len(sessions), "managed_devices": len(devices),
		"online_devices": onlineDevices, "offline_devices": len(devices) - onlineDevices,
		"hbbs_instances": hbbsInstances, "hbbr_instances": hbbrInstances,
		"rendezvous_peers": rendezvousPeers, "relay_servers": len(relays),
		"healthy_relays": healthyRelays, "relay_connections": relayConnections,
		"relay_bandwidth": relayBandwidth, "cpu_percent": cpuPercent,
		"cpu_count": logicalCPUs(), "memory_bytes": memoryBytes,
		"memory_cgroup_bytes": memoryCgroupBytes,
		"memory_limit_bytes":  memoryLimit, "uptime_seconds": uptimeSeconds,
		"history": history,
	})
}

func (s *Server) userPresence(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	users, sessions, err := s.repository.ListAuthState(request.Context(), now)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "user presence unavailable")
		return
	}
	writeJSON(response, http.StatusOK, presence.Calculate(users, sessions, now))
}

func envValue(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value != "0" && value != "false" && value != "off"
}

func validUsername(username string) bool {
	if len(username) < 2 || len(username) > 128 {
		return false
	}
	for _, character := range username {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validEmail(email string) bool {
	if email == "" {
		return true
	}
	if len(email) > 320 {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}

func (s *Server) authSnapshot(response http.ResponseWriter, request *http.Request) {
	for attempt := 0; attempt < 3; attempt++ {
		revisionBefore := s.hub.Revision()
		users, sessions, err := s.repository.ListAuthState(request.Context(), time.Now().UTC())
		if err != nil {
			writeError(response, http.StatusInternalServerError, "snapshot unavailable")
			return
		}
		devices, deviceErr := s.repository.ListDevices(request.Context())
		aclRules, aclErr := s.repository.ListACLRules(request.Context())
		strategies, strategyErr := s.repository.ListStrategies(request.Context())
		memberships, membershipErr := s.repository.ListUserGroupMemberships(request.Context())
		relays, relayErr := s.repository.ListRelayServers(request.Context())
		if deviceErr != nil || aclErr != nil || strategyErr != nil || membershipErr != nil || relayErr != nil {
			writeError(response, http.StatusInternalServerError, "policy snapshot unavailable")
			return
		}
		revisionAfter := s.hub.Revision()
		if revisionBefore == revisionAfter {
			configured := s.runtimeConfiguration()
			writeJSON(response, http.StatusOK, domain.AuthSnapshot{SourceID: s.hub.SourceID(), Revision: revisionAfter,
				RequireLogin: configured.RequireLogin, RequireDeviceDeployment: configured.RequireDeviceDeployment,
				CreatedAt: time.Now().UTC(), Users: users, Sessions: sessions, Devices: devices,
				ACLRules: aclRules, Strategies: strategies, UserGroupMemberships: memberships, RelayServers: relays})
			return
		}
	}
	writeError(response, http.StatusServiceUnavailable, "snapshot changed during capture; retry")
}

func (s *Server) authEvents(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	eventsChannel, backlog, unsubscribe := s.hub.SubscribeAfter(request.URL.Query().Get("source_id"), after)
	defer unsubscribe()
	for _, event := range backlog {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(response, "data: %s\n\n", data)
	}
	flusher.Flush()
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case event, open := <-eventsChannel:
			if !open {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(response, "id: %d\nevent: auth\ndata: %s\n\n", event.Revision, data)
			flusher.Flush()
		case <-keepAlive.C:
			fmt.Fprint(response, ": keepalive\n\n")
			flusher.Flush()
		case <-request.Context().Done():
			return
		}
	}
}

func (s *Server) connectionAudit(response http.ResponseWriter, request *http.Request) {
	var event domain.AuditEvent
	if err := decodeJSON(request, &event, 64<<10); err != nil {
		writeError(response, http.StatusBadRequest, "invalid audit event")
		return
	}
	if event.Type != "connection_allowed" && event.Type != "connection_denied" {
		writeError(response, http.StatusBadRequest, "invalid audit event type")
		return
	}
	if event.ActorUserID != "" {
		if user, err := s.repository.FindUserByID(request.Context(), event.ActorUserID); err == nil {
			if event.Metadata == nil {
				event.Metadata = make(map[string]any)
			}
			event.Metadata["controller_login"] = user.Username
			event.Metadata["controller_display_name"] = displayName(user)
		}
	}
	if lookup, ok := s.repository.(interface {
		DeviceAuditLabel(context.Context, string) (string, string, error)
	}); ok && event.TargetRustDeskID != "" {
		if hostname, alias, err := lookup.DeviceAuditLabel(request.Context(), event.TargetRustDeskID); err == nil {
			if event.Metadata == nil {
				event.Metadata = make(map[string]any)
			}
			event.Metadata["target_hostname"] = hostname
			event.Metadata["target_alias"] = alias
		}
	}
	if err := s.audit.Record(request.Context(), event); err != nil {
		writeError(response, http.StatusInternalServerError, "audit unavailable")
		return
	}
	writeJSON(response, http.StatusAccepted, nil)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(response, http.StatusUnauthorized, "authentication required")
			return
		}
		user, session, claims, err := s.auth.Authenticate(request.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if err != nil {
			writeError(response, http.StatusUnauthorized, authErrorMessage(err))
			return
		}
		next.ServeHTTP(response, request.WithContext(withPrincipal(request.Context(), Principal{User: user, Session: session, Claims: claims})))
	})
}

func (s *Server) requirePermission(permission string, next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		principal, _ := principalFrom(request.Context())
		if !hasPermission(principal.User, permission) {
			writeError(response, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(response, request)
	}))
}

func hasPermission(user domain.User, permission string) bool {
	if user.Role == domain.RoleAdmin {
		return true
	}
	for _, candidate := range user.Permissions {
		if candidate == domain.PermissionAll || candidate == permission {
			return true
		}
	}
	return false
}

func (s *Server) requireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("X-RDS-Internal-Token")
		if header == "" {
			header = request.Header.Get("X-ART-Internal-Token")
		}
		candidate := []byte(header)
		if len(candidate) != len(s.internalSecret) || subtle.ConstantTimeCompare(candidate, s.internalSecret) != 1 {
			writeError(response, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) requireBuilder(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		header := strings.TrimSpace(request.Header.Get("Authorization"))
		candidate := []byte(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if len(s.builderToken) < 32 || len(candidate) != len(s.builderToken) || subtle.ConstantTimeCompare(candidate, s.builderToken) != 1 {
			writeError(response, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) requireBuilderWorker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		header := strings.TrimSpace(request.Header.Get("Authorization"))
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		worker, err := findBuilderWorkerByToken(request.Context(), s.repository, token)
		if err != nil {
			writeError(response, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), builderWorkerContextKey{}, worker.ID)))
	})
}

func authErrorMessage(err error) string {
	switch {
	case errors.Is(err, auth.ErrTokenExpired), errors.Is(err, auth.ErrSessionExpired):
		return "session expired"
	case errors.Is(err, auth.ErrSessionRevoked):
		return "session revoked"
	case errors.Is(err, auth.ErrUserDisabled):
		return "user disabled"
	case errors.Is(err, auth.ErrApprovalRejected):
		return "registration rejected"
	case errors.Is(err, auth.ErrStaleToken):
		return "force re-login required"
	default:
		return "invalid token"
	}
}

func clientUser(user domain.User) map[string]any {
	return map[string]any{"id": user.ID, "name": displayName(user), "username": user.Username,
		"email": user.Email, "phone": user.Phone, "display_name": user.DisplayName, "status": boolStatus(user.Enabled),
		"avatar": "/api/avatar/" + user.ID, "enabled": user.Enabled, "approval_status": user.ApprovalStatus, "is_admin": user.Role == domain.RoleAdmin, "role": user.Role, "permissions": user.Permissions, "totp_enabled": user.TOTPEnabled, "note": ""}
}

func displayName(user domain.User) string {
	if strings.TrimSpace(user.DisplayName) != "" {
		return user.DisplayName
	}
	return user.Username
}

func boolStatus(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}

func decodeJSON(request *http.Request, target any, limit int64) error {
	limited := &io.LimitedReader{R: request.Body, N: limit + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	if limited.N <= 0 || request.ContentLength > limit {
		return errors.New("JSON body too large")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}

func clientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(response, request)
	})
}

func trustedProxyHeaders(trusted []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		peer := clientIP(request)
		peerAddress, err := netip.ParseAddr(peer)
		if err != nil || !prefixContains(trusted, peerAddress.Unmap()) {
			request.Header.Del("X-Forwarded-For")
			request.Header.Del("X-Real-IP")
			next.ServeHTTP(response, request)
			return
		}
		chain := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
		for index := len(chain) - 1; index >= 0; index-- {
			candidate, parseErr := netip.ParseAddr(strings.TrimSpace(chain[index]))
			if parseErr != nil {
				continue
			}
			candidate = candidate.Unmap()
			if !prefixContains(trusted, candidate) {
				request.RemoteAddr = net.JoinHostPort(candidate.String(), "0")
				break
			}
		}
		next.ServeHTTP(response, request)
	})
}

func prefixContains(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(response, request)
		slog.Debug("http request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started))
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("http panic", "error", recovered)
				writeError(response, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(response, request)
	})
}
