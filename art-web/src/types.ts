export interface CurrentUser {
  id: string
  name: string
  username: string
  email: string
  phone: string
  display_name: string
  avatar: string
	role: string
	permissions: string[]
  is_admin: boolean
  enabled: boolean
  approval_status: 'pending'|'approved'|'rejected'
  status: number
  totp_enabled: boolean
}

export interface ManagedUser extends CurrentUser { group_ids:string[] }

export interface LoginResponse {
  access_token: string
  expires_at: string
  type: string
  user: CurrentUser
}

export interface Device { rustdesk_id: string; client_uuid: string; hostname: string; alias: string; platform: string; version: string; cpu: string; memory: string; username: string; last_seen_ip: string; online: boolean; last_seen: string; owner_user_id: string; group_id: string; tags: string[]; deployed:boolean; deployed_by?:string; deployed_at?:string; archived_at?:string; created_at: string }
export interface APIToken { id:string; user_id:string; name:string; prefix:string; scopes:string[]; created_at:string; expires_at:string; last_used_at?:string; revoked_at?:string }
export interface Webhook { id:string; name:string; url:string; events:string[]; enabled:boolean; created_at:string; updated_at:string }
export interface WebhookDelivery { id:string; webhook_id:string; event_type:string; status:'pending'|'delivered'|'failed'|'cancelled'; attempts:number; response_code:number; last_error:string; next_attempt:string; created_at:string; delivered_at?:string }
export interface Notification { id:string; type:string; severity:'info'|'warning'|'critical'; title:string; message:string; resource?:string; created_at:string; read_at?:string }
export interface AutomationRule { id:string; name:string; event_types:string[]; conditions:Record<string,string>; actions:string[]; severity:'info'|'warning'|'critical'; throttle_seconds:number; enabled:boolean; created_at:string; updated_at:string }
export interface AutomationRun { id:string; rule_id:string; event_type:string; event_id:string; status:string; message:string; created_at:string }
export interface ClusterNode { id:string; service:string; version:string; address:string; started_at:string; last_seen_at:string; lease_count:number }
export interface ClusterLease { name:string; owner_id:string; expires_at:string; updated_at:string }
export interface ClusterState { nodes:ClusterNode[]; leases:ClusterLease[]; generated_at:string }
export interface ManagedGroup { id: string; name: string; description: string; kind: 'user' | 'device'; created_at: string; updated_at: string }
export interface Session { id: string; user_id: string; expires_at: string; revoked_at?: string; last_seen_at: string; ip: string; user_agent: string; client_device_id: string }
export interface SessionRecord extends Session { created_at:string; username:string; display_name:string; user_enabled:boolean; status:'active'|'revoked'|'expired'; current:boolean }
export interface SessionPage { sessions:SessionRecord[]; total:number; limit:number; offset:number }
export interface SessionSummary { total:number; active:number; revoked:number; expired:number }
export interface AuditEvent { id: string; occurred_at: string; type: string; actor_user_id?: string; actor_session_id?: string; controller_device_id?: string; target_rustdesk_id?: string; ip?: string; result?: string; reason?: string; metadata?:Record<string,unknown> }
export interface AuditPage { events:AuditEvent[]; total:number; limit:number; offset:number }
export interface AuditSummary { total:number; allowed_connections:number; denied_connections:number; failed_logins:number; by_type:Record<string,number>; by_result:Record<string,number> }
export interface AuditQuery { limit?:number; offset?:number; type?:string; result?:string; actor_user_id?:string; target_id?:string; ip?:string; search?:string; from?:string; to?:string }
export interface RuntimeSettings { require_login: boolean; require_device_deployment:boolean; database_driver: string; access_token_ttl: string; session_ttl: string; login_burst: string; login_window: string; login_lockout: string; device_online_ttl: string; relay_server: string; server_public_key: string; mfa_mode: 'optional'|'required_for_admins'|'required_for_all_users'; password_minimum_length:number; password_require_upper:boolean; password_require_lower:boolean; password_require_number:boolean; password_require_special:boolean; oidc_enabled:boolean; oidc_provider:string; registration_enabled:boolean; registration_auto_approve:boolean; ldap_enabled:boolean; ldap_auto_provision:boolean; custom_logo:boolean; logo_url:string; custom_global_avatar:boolean; global_avatar_url:string }
export interface TOTPEnrollment { secret:string; otpauth_uri:string; recovery_codes:string[] }
export interface OIDCIdentity { provider:string; subject:string; user_id:string; email:string; created_at:string }
export interface Infrastructure {
  api:string; hbbs:string; hbbr:string; database:string; database_driver:string
  api_address:string; hbbs_address:string; hbbr_address:string
  users:number; active_sessions:number; managed_devices:number; online_devices:number; offline_devices:number
  hbbs_instances:number; hbbr_instances:number; rendezvous_peers:number; relay_servers:number
  healthy_relays:number; relay_connections:number; relay_bandwidth:number
  cpu_percent:number; cpu_count:number; memory_bytes:number; memory_cgroup_bytes:number; memory_limit_bytes:number; uptime_seconds:number
  history:InfrastructureSample[]
}
export interface InfrastructureSample { timestamp:string; cpu_percent:number; memory_bytes:number; online_devices:number; active_sessions:number; relay_connections:number; relay_bandwidth:number }
export interface ServiceCommand { id:string; service:string; target_instance:string; type:string; created_at:string; expires_at:string; acknowledged_at?:string; acknowledged_by?:string }
export interface UserPresence { user_id:string; username:string; display_name:string; state:'online'|'idle'|'offline'|'pending'|'disabled'; last_seen_at?:string; client_device_id?:string; active_devices:number }
export interface PresenceSnapshot { online:number; idle:number; offline:number; users:UserPresence[] }
export interface LiveConnection { key:string; status:'active'|'stale'|'closed'; actor_user_id?:string; actor_session_id?:string; controller_device_id?:string; controller_name?:string; controller_login?:string; target_rustdesk_id:string; connection_type:number; ip?:string; started_at:string; last_seen_at:string; closed_at?:string; duration_seconds:number }
export interface LiveConnectionSnapshot { active:number; stale:number; closed:number; items:LiveConnection[] }
export interface ConnectionContainment { status:'contained'|'already_closed'; session_revoked?:boolean; new_connections_blocked?:boolean; transport_interrupted?:boolean; connection:LiveConnection }
export interface DiagnosticCheck { name:string; status:'ok'|'warning'|'error'; message:string }
export interface Diagnostics { status:'ok'|'warning'|'error'; checked_at:string; checks:DiagnosticCheck[]; auth_cache_source_id:string; auth_cache_revision:number; trusted_proxy_count:number }
export interface DashboardLayout { version:number; order:string[]; hidden:string[] }
export interface BackupInspection { valid:boolean; size_bytes:number; modified_at:string; quick_check:string; schema_tables:number; users:number; devices:number; sessions:number }
export interface BackupArtifact { name:string; size_bytes:number; created_at:string; users:number; devices:number; sessions:number; quick_check:string }
export interface ClientProfile { id:string; name:string; description:string; platform:'all'|'windows'|'linux'|'macos'|'android'; settings:Record<string,unknown>; branding:Record<string,unknown>; version:number; enabled:boolean; created_at:string; updated_at:string }
export interface ClientProfileAssignment { id:string; profile_id:string; scope_type:'global'|'user'|'user_group'|'device_group'|'device'; scope_id:string; priority:number; created_at:string }
export interface ClientProfileBundle { schema:string; profile:ClientProfile; issued_at:string; signature:string }
export interface ClientBuild { id:string; profile_id:string; target_os:string; architecture:string; format:string; status:string; artifact_name:string; media_type?:string; sha256:string; error:string; created_by:string; worker_id?:string; attempts:number; created_at:string; started_at?:string; lease_until?:string; completed_at?:string }
export interface BuilderWorker { id:string; name:string; hostname:string; version:string; formats:string[]; platforms:string[]; architectures:string[]; concurrency:number; status:'online'|'offline'; last_seen_at:string; created_at:string; updated_at:string }
export interface AddressBook { id:string; name:string; kind:'personal'|'shared'; owner_user_id:string; permission:'read'|'write'|'manage'; can_manage:boolean; created_at:string; updated_at:string }
export interface AddressBookGrant { id:string; address_book_id:string; subject_type:'user'|'user_group'; subject_id:string; permission:'read'|'write'; created_at:string; updated_at:string }
export interface AddressBookEntry { id:string; address_book_id:string; rustdesk_id:string; alias:string; folder:string; favourite:boolean; created_at:string }
export interface RelayServer { id:string; name:string; hostname:string; port:number; region:string; enabled:boolean; health:string; latency_ms:number; connections:number; bandwidth:number; created_at:string; updated_at:string }
export interface RelayMetric { relay_id:string; recorded_at:string; health:string; latency_ms:number; connections:number; bandwidth:number }
export interface ACLRule { id:string; name:string; subject_type:string; subject_id:string; target_type:string; target_id:string; permissions:string[]; effect:'allow'|'deny'; enabled:boolean; priority:number }
export interface ACLRuleTrace { rule_id:string; name:string; priority:number; effect:'allow'|'deny'; subject_matched:boolean; target_matched:boolean; permission_matched:boolean; matched:boolean }
export interface ACLEvaluation { allowed:boolean; reason:string; winning_effect:string; winning_priority:number; matched_rule_ids:string[]; matched_rules:ACLRuleTrace[]; enabled_rule_count:number; user_id:string; user_group_ids:string[]; target_id:string; target_group_id:string; permission:string }
export interface Strategy { id:string; name:string; scope_type:string; scope_id:string; priority:number; settings:Record<string,unknown>; enabled:boolean }
export interface StrategySettingDefinition { key:string; label:string; label_en:string; category:string; kind:'boolean'|'string'; server_only:boolean }
export interface StrategyEvaluation { modified_at:number; config_options:Record<string,string>; effective_settings:Record<string,unknown>; matched_strategy_ids:string[] }
export interface RoleDefinition { id:string; name:string; description:string; permissions:string[]; system:boolean; created_at:string; updated_at:string }
