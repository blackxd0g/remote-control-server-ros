use std::{
    collections::HashMap,
    fs,
    path::Path,
    sync::RwLock,
    time::{SystemTime, UNIX_EPOCH},
};

use serde::{Deserialize, Serialize};
use thiserror::Error;
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

use super::{Claims, JwtVerifier};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct UserState {
    pub id: String,
    pub username: String,
    pub role: String,
    pub enabled: bool,
    #[serde(default = "default_approval_status")]
    pub approval_status: String,
    pub token_version: i64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct SessionState {
    pub id: String,
    pub user_id: String,
    pub expires_at: String,
    #[serde(default)]
    pub revoked_at: Option<String>,
    #[serde(default)]
    pub client_device_id: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct AuthSnapshot {
    #[serde(default)]
    pub require_login: Option<bool>,
    #[serde(default)]
    pub require_device_deployment: Option<bool>,
    #[serde(default)]
    pub source_id: String,
    pub revision: i64,
    pub users: Vec<UserState>,
    pub sessions: Vec<SessionState>,
    #[serde(default)]
    pub devices: Vec<DeviceState>,
    #[serde(default)]
    pub acl_rules: Vec<AclRuleState>,
    #[serde(default)]
    pub strategies: Vec<StrategyState>,
    #[serde(default)]
    pub user_group_memberships: Vec<UserGroupMembershipState>,
    #[serde(default)]
    pub relay_servers: Vec<RelayState>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct UserGroupMembershipState {
    pub group_id: String,
    pub user_id: String,
    #[serde(default = "default_active")]
    pub active: bool,
}

fn default_active() -> bool {
    true
}

fn default_approval_status() -> String {
    "approved".to_owned()
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct DeviceState {
    pub rustdesk_id: String,
    #[serde(default)]
    pub client_uuid: String,
    #[serde(default)]
    pub public_key: String,
    #[serde(default)]
    pub deployed: bool,
    #[serde(default)]
    pub owner_user_id: String,
    #[serde(default)]
    pub group_id: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct AclRuleState {
    pub id: String,
    pub subject_type: String,
    #[serde(default)]
    pub subject_id: String,
    pub target_type: String,
    #[serde(default)]
    pub target_id: String,
    pub permissions: Vec<String>,
    #[serde(default = "default_acl_effect")]
    pub effect: String,
    pub enabled: bool,
    pub priority: i32,
    #[serde(default)]
    pub deleted: bool,
}

fn default_acl_effect() -> String {
    "allow".to_owned()
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct StrategyState {
    pub id: String,
    pub scope_type: String,
    #[serde(default)]
    pub scope_id: String,
    pub priority: i32,
    pub settings: serde_json::Map<String, serde_json::Value>,
    pub enabled: bool,
    #[serde(default)]
    pub deleted: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct RelayState {
    pub id: String,
    pub hostname: String,
    pub port: u16,
    #[serde(default)]
    pub region: String,
    pub enabled: bool,
    pub health: String,
    #[serde(default)]
    pub latency_ms: i32,
    #[serde(default)]
    pub connections: i32,
    #[serde(default)]
    pub deleted: bool,
}

#[derive(Default, Serialize, Deserialize)]
struct CacheState {
    #[serde(default)]
    require_login: Option<bool>,
    #[serde(default)]
    require_device_deployment: Option<bool>,
    #[serde(default)]
    source_id: String,
    revision: i64,
    users: HashMap<String, UserState>,
    sessions: HashMap<String, SessionState>,
    #[serde(default)]
    devices: HashMap<String, DeviceState>,
    #[serde(default)]
    acl_rules: HashMap<String, AclRuleState>,
    #[serde(default)]
    strategies: HashMap<String, StrategyState>,
    #[serde(default)]
    user_groups: HashMap<String, Vec<String>>,
    #[serde(default)]
    relay_servers: HashMap<String, RelayState>,
}

#[derive(Clone, Debug)]
pub struct AuthDecision {
    pub user_id: String,
    pub session_id: String,
    pub username: String,
    pub role: String,
    pub controller_device_id: String,
}

#[derive(Clone, Copy, Debug, Error, PartialEq, Eq)]
pub enum AuthError {
    #[error("Для подключения необходимо войти в аккаунт RustDesk")]
    LoginRequired,
    #[error("Connection denied: invalid token")]
    InvalidToken,
    #[error("Connection denied: session expired")]
    SessionExpired,
    #[error("Connection denied: session revoked")]
    SessionRevoked,
    #[error("Connection denied: user disabled")]
    UserDisabled,
    #[error("Ваша учётная запись ожидает подтверждения администратора")]
    ApprovalPending,
    #[error("Учётная запись отклонена администратором")]
    ApprovalRejected,
    #[error("Connection denied: force re-login required")]
    ForceRelogin,
    #[error("Connection denied: access is not permitted by policy")]
    AccessDenied,
    #[error("Connection denied: connection type is disabled by strategy")]
    StrategyDenied,
}

pub struct AuthCache {
    verifier: JwtVerifier,
    require_login: bool,
    state: RwLock<CacheState>,
}

impl AuthCache {
    pub fn new(verifier: JwtVerifier, require_login: bool) -> Self {
        Self {
            verifier,
            require_login,
            state: RwLock::new(CacheState::default()),
        }
    }

    pub fn authorize(&self, token: Option<&str>) -> Result<Option<AuthDecision>, AuthError> {
        let token = match token.filter(|value| !value.trim().is_empty()) {
            Some(value) => value,
            None if self
                .state
                .read()
                .map(|state| state.require_login.unwrap_or(self.require_login))
                .unwrap_or(self.require_login) =>
            {
                return Err(AuthError::LoginRequired);
            }
            None => return Ok(None),
        };
        let claims = self.verifier.verify(token)?;
        self.authorize_claims(&claims).map(Some)
    }

    fn authorize_claims(&self, claims: &Claims) -> Result<AuthDecision, AuthError> {
        let state = self.state.read().map_err(|_| AuthError::InvalidToken)?;
        let user = state
            .users
            .get(&claims.sub)
            .ok_or(AuthError::SessionRevoked)?;
        if !user.enabled {
            return Err(AuthError::UserDisabled);
        }
        if user.approval_status == "pending" {
            return Err(AuthError::ApprovalPending);
        }
        if user.approval_status != "approved" {
            return Err(AuthError::ApprovalRejected);
        }
        let session = state
            .sessions
            .get(&claims.sid)
            .ok_or(AuthError::SessionRevoked)?;
        if session.user_id != user.id {
            return Err(AuthError::SessionRevoked);
        }
        if session.revoked_at.is_some() {
            return Err(AuthError::SessionRevoked);
        }
        if user.token_version != claims.token_version {
            return Err(AuthError::ForceRelogin);
        }
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs();
        let session_expiry = OffsetDateTime::parse(&session.expires_at, &Rfc3339)
            .map_err(|_| AuthError::SessionExpired)?;
        if claims.exp <= now || session_expiry.unix_timestamp() <= now as i64 {
            return Err(AuthError::SessionExpired);
        }
        Ok(AuthDecision {
            user_id: user.id.clone(),
            session_id: session.id.clone(),
            username: user.username.clone(),
            role: user.role.clone(),
            controller_device_id: if claims.device_id.is_empty() {
                session.client_device_id.clone()
            } else {
                claims.device_id.clone()
            },
        })
    }

    pub fn replace(&self, snapshot: AuthSnapshot) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if snapshot.source_id == state.source_id && snapshot.revision < state.revision {
            return Ok(());
        }
        state.source_id = snapshot.source_id;
        state.revision = snapshot.revision;
        state.require_login = snapshot.require_login;
        state.require_device_deployment = snapshot.require_device_deployment;
        state.users = snapshot
            .users
            .into_iter()
            .map(|user| (user.id.clone(), user))
            .collect();
        state.sessions = snapshot
            .sessions
            .into_iter()
            .map(|session| (session.id.clone(), session))
            .collect();
        state.devices = snapshot
            .devices
            .into_iter()
            .map(|value| (value.rustdesk_id.clone(), value))
            .collect();
        state.acl_rules = snapshot
            .acl_rules
            .into_iter()
            .map(|value| (value.id.clone(), value))
            .collect();
        state.strategies = snapshot
            .strategies
            .into_iter()
            .map(|value| (value.id.clone(), value))
            .collect();
        state.relay_servers = snapshot
            .relay_servers
            .into_iter()
            .map(|value| (value.id.clone(), value))
            .collect();
        state.user_groups.clear();
        for membership in snapshot.user_group_memberships {
            if membership.active {
                state
                    .user_groups
                    .entry(membership.user_id)
                    .or_default()
                    .push(membership.group_id);
            }
        }
        Ok(())
    }

    pub fn apply_configuration(
        &self,
        require_login: bool,
        require_device_deployment: bool,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            state.require_login = Some(require_login);
            state.require_device_deployment = Some(require_device_deployment);
        }
        Ok(())
    }

    pub fn require_device_deployment(&self, fallback: bool) -> bool {
        self.state
            .read()
            .map(|state| state.require_device_deployment.unwrap_or(fallback))
            .unwrap_or(fallback)
    }

    pub fn authorize_policy(
        &self,
        decision: &AuthDecision,
        target: &str,
        connection_type: i32,
    ) -> Result<bool, AuthError> {
        let state = self.state.read().map_err(|_| AuthError::AccessDenied)?;
        let user_groups = state
            .user_groups
            .get(&decision.user_id)
            .map(Vec::as_slice)
            .unwrap_or(&[]);
        if decision.role != "admin" && !state.acl_rules.is_empty() {
            let permission = permission_for(connection_type);
            let device_group = state
                .devices
                .get(target)
                .map(|value| value.group_id.as_str())
                .unwrap_or("");
            let mut matching: Vec<_> = state
                .acl_rules
                .values()
                .filter(|rule| rule.enabled)
                .filter(|rule| {
                    let subject_matches = rule.subject_id.is_empty()
                        || (rule.subject_type == "user" && rule.subject_id == decision.user_id)
                        || (rule.subject_type == "user_group"
                            && user_groups.contains(&rule.subject_id));
                    let target_matches = rule.target_id.is_empty()
                        || (rule.target_type == "device" && rule.target_id == target)
                        || (rule.target_type == "device_group"
                            && !device_group.is_empty()
                            && rule.target_id == device_group);
                    subject_matches
                        && target_matches
                        && rule.permissions.iter().any(|value| value == permission)
                })
                .collect();
            matching.sort_by_key(|rule| (rule.priority, rule.effect != "deny"));
            if matching.first().map(|rule| rule.effect.as_str()) != Some("allow") {
                return Err(AuthError::AccessDenied);
            }
        }
        let device_group = state
            .devices
            .get(target)
            .map(|value| value.group_id.as_str())
            .unwrap_or("");
        let mut strategies: Vec<_> = state
            .strategies
            .values()
            .filter(|strategy| {
                strategy.enabled
                    && strategy_matches(strategy, decision, target, device_group, user_groups)
            })
            .collect();
        strategies.sort_by_key(|strategy| (strategy.priority, strategy_specificity(strategy)));
        let permission_key = strategy_permission_for(connection_type);
        let mut effective_permission = None;
        let mut force_relay = false;
        for strategy in &strategies {
            if let Some(value) = strategy
                .settings
                .get(permission_key)
                .and_then(|value| value.as_bool())
            {
                effective_permission = Some(value);
            }
            if let Some(value) = strategy
                .settings
                .get("force_relay")
                .and_then(|value| value.as_bool())
            {
                force_relay = value;
            }
        }
        if effective_permission == Some(false) {
            return Err(AuthError::StrategyDenied);
        }
        Ok(force_relay)
    }

    pub fn device_registration_allowed(&self, id: &str, public_key: &str) -> bool {
        let Ok(state) = self.state.read() else {
            return false;
        };
        state.devices.get(id).is_some_and(|device| {
            device.deployed && !device.public_key.is_empty() && device.public_key == public_key
        })
    }

    pub fn upsert_device(
        &self,
        value: DeviceState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            state.devices.insert(value.rustdesk_id.clone(), value);
        }
        Ok(())
    }

    pub fn upsert_acl(
        &self,
        value: AclRuleState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            if value.deleted {
                state.acl_rules.remove(&value.id);
            } else {
                state.acl_rules.insert(value.id.clone(), value);
            }
        }
        Ok(())
    }
    pub fn upsert_strategy(
        &self,
        value: StrategyState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            if value.deleted {
                state.strategies.remove(&value.id);
            } else {
                state.strategies.insert(value.id.clone(), value);
            }
        }
        Ok(())
    }

    pub fn upsert_relay(
        &self,
        value: RelayState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            if value.deleted {
                state.relay_servers.remove(&value.id);
            } else {
                state.relay_servers.insert(value.id.clone(), value);
            }
        }
        Ok(())
    }

    pub fn select_relay(&self) -> Option<String> {
        let state = self.state.read().ok()?;
        state
            .relay_servers
            .values()
            .filter(|value| value.enabled && value.health == "healthy")
            .min_by_key(|value| (value.connections, value.latency_ms, &value.id))
            .map(|value| format_relay_address(&value.hostname, value.port))
    }

    pub fn upsert_user_group_membership(
        &self,
        value: UserGroupMembershipState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            let groups = state.user_groups.entry(value.user_id).or_default();
            groups.retain(|group_id| group_id != &value.group_id);
            if value.active {
                groups.push(value.group_id);
            }
        }
        Ok(())
    }

    pub fn upsert_user(
        &self,
        user: UserState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            state.users.insert(user.id.clone(), user);
        }
        Ok(())
    }

    pub fn upsert_session(
        &self,
        session: SessionState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            state.sessions.insert(session.id.clone(), session);
        }
        Ok(())
    }

    pub fn revoke_session(
        &self,
        session: SessionState,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        self.upsert_session(session, source_id, revision)
    }

    pub fn revoke_user_sessions(
        &self,
        user_id: &str,
        source_id: &str,
        revision: i64,
    ) -> anyhow::Result<()> {
        let mut state = self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        if accept_revision(&mut state, source_id, revision) {
            state
                .sessions
                .retain(|_, session| session.user_id != user_id);
        }
        Ok(())
    }

    pub fn save(&self, path: &Path) -> anyhow::Result<()> {
        let state = self
            .state
            .read()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))?;
        let data = serde_json::to_vec(&*state)?;
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        let temporary = path.with_extension("tmp");
        fs::write(&temporary, data)?;
        fs::rename(temporary, path)?;
        Ok(())
    }

    pub fn load(&self, path: &Path) -> anyhow::Result<()> {
        let data = fs::read(path)?;
        let loaded: CacheState = serde_json::from_slice(&data)?;
        *self
            .state
            .write()
            .map_err(|_| anyhow::anyhow!("auth cache lock poisoned"))? = loaded;
        Ok(())
    }

    pub fn revision(&self) -> i64 {
        self.state
            .read()
            .map(|state| state.revision)
            .unwrap_or_default()
    }

    pub fn cursor(&self) -> (String, i64) {
        self.state
            .read()
            .map(|state| (state.source_id.clone(), state.revision))
            .unwrap_or_default()
    }
}

fn format_relay_address(hostname: &str, port: u16) -> String {
    if hostname.contains(':') && !hostname.starts_with('[') {
        format!("[{hostname}]:{port}")
    } else {
        format!("{hostname}:{port}")
    }
}

fn permission_for(connection_type: i32) -> &'static str {
    match connection_type {
        1 => "file_transfer",
        2 | 3 => "tcp_tunnel",
        4 => "camera",
        5 => "terminal",
        _ => "remote_control",
    }
}
fn strategy_permission_for(connection_type: i32) -> &'static str {
    match connection_type {
        1 => "allow_file_transfer",
        2 | 3 => "allow_tcp_tunnel",
        4 => "allow_camera",
        5 => "allow_terminal",
        _ => "allow_remote_control",
    }
}
fn strategy_matches(
    value: &StrategyState,
    decision: &AuthDecision,
    target: &str,
    device_group: &str,
    user_groups: &[String],
) -> bool {
    match value.scope_type.as_str() {
        "global" => true,
        "user" => value.scope_id == decision.user_id,
        "device" => value.scope_id == target,
        "device_group" => !device_group.is_empty() && value.scope_id == device_group,
        "user_group" => user_groups.contains(&value.scope_id),
        _ => value.scope_id.is_empty(),
    }
}
fn strategy_specificity(value: &StrategyState) -> i32 {
    match value.scope_type.as_str() {
        "device" => 0,
        "user" => 1,
        "device_group" => 2,
        "user_group" => 3,
        _ => 4,
    }
}

fn accept_revision(state: &mut CacheState, source_id: &str, revision: i64) -> bool {
    if state.source_id != source_id {
        state.source_id = source_id.to_owned();
        state.revision = revision;
        return true;
    }
    if revision > state.revision {
        state.revision = revision;
        return true;
    }
    false
}

#[cfg(test)]
mod tests {
    use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
    use hmac::{Hmac, Mac};
    use sha2::Sha256;

    use super::*;
    use crate::auth::Audience;

    const SECRET: &[u8] = b"0123456789abcdef0123456789abcdef0123456789abcdef";

    fn token(exp: u64) -> String {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let header = URL_SAFE_NO_PAD.encode(br#"{"alg":"HS256","typ":"JWT"}"#);
        let payload = URL_SAFE_NO_PAD.encode(
            serde_json::to_vec(&Claims {
                sub: "user-1".into(),
                sid: "session-1".into(),
                iat: now,
                exp,
                iss: "art-rustdesk".into(),
                aud: Audience::Many(vec!["art-hbbs".into()]),
                username: "alice".into(),
                role: "user".into(),
                token_version: 3,
                device_id: "controller-1".into(),
            })
            .unwrap(),
        );
        let signed = format!("{header}.{payload}");
        let mut mac = Hmac::<Sha256>::new_from_slice(SECRET).unwrap();
        mac.update(signed.as_bytes());
        format!(
            "{signed}.{}",
            URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes())
        )
    }

    fn cache() -> AuthCache {
        let cache = AuthCache::new(
            JwtVerifier::hs256(SECRET, "art-rustdesk", "art-hbbs").unwrap(),
            true,
        );
        cache
            .replace(AuthSnapshot {
                require_login: Some(true),
                require_device_deployment: Some(false),
                source_id: "api-run-1".into(),
                revision: 1,
                users: vec![UserState {
                    id: "user-1".into(),
                    username: "alice".into(),
                    role: "user".into(),
                    enabled: true,
                    approval_status: "approved".into(),
                    token_version: 3,
                }],
                sessions: vec![SessionState {
                    id: "session-1".into(),
                    user_id: "user-1".into(),
                    expires_at: "2099-01-01T00:00:00Z".into(),
                    revoked_at: None,
                    client_device_id: "controller-1".into(),
                }],
                ..Default::default()
            })
            .unwrap();
        cache
    }

    #[test]
    fn login_is_required_before_connection_lookup() {
        assert_eq!(
            cache().authorize(None).unwrap_err(),
            AuthError::LoginRequired
        );
    }

    #[test]
    fn active_server_side_session_is_required() {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let cache = cache();
        assert_eq!(
            cache
                .authorize(Some(&token(now + 3600)))
                .unwrap()
                .unwrap()
                .user_id,
            "user-1"
        );
        cache
            .revoke_user_sessions("user-1", "api-run-1", 2)
            .unwrap();
        assert_eq!(
            cache.authorize(Some(&token(now + 3600))).unwrap_err(),
            AuthError::SessionRevoked
        );
    }

    #[test]
    fn runtime_login_requirement_is_applied_from_api_events() {
        let cache = cache();
        assert_eq!(cache.authorize(None).unwrap_err(), AuthError::LoginRequired);
        cache
            .apply_configuration(false, false, "api-run-1", 2)
            .unwrap();
        assert!(cache.authorize(None).unwrap().is_none());
        cache
            .apply_configuration(true, false, "api-run-1", 3)
            .unwrap();
        assert_eq!(cache.authorize(None).unwrap_err(), AuthError::LoginRequired);
    }

    #[test]
    fn deployed_device_requires_matching_public_key() {
        let cache = cache();
        cache
            .upsert_device(
                DeviceState {
                    rustdesk_id: "226424246".into(),
                    client_uuid: "uuid".into(),
                    public_key: "expected-key".into(),
                    deployed: true,
                    owner_user_id: "user-1".into(),
                    group_id: String::new(),
                },
                "api-run-1",
                2,
            )
            .unwrap();
        assert!(cache.device_registration_allowed("226424246", "expected-key"));
        assert!(!cache.device_registration_allowed("226424246", "wrong-key"));
        assert!(!cache.device_registration_allowed("unknown", "expected-key"));
    }

    #[test]
    fn pending_registration_is_denied_before_connection_lookup() {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let cache = cache();
        cache
            .upsert_user(
                UserState {
                    id: "user-1".into(),
                    username: "alice".into(),
                    role: "user".into(),
                    enabled: true,
                    approval_status: "pending".into(),
                    token_version: 3,
                },
                "api-run-1",
                2,
            )
            .unwrap();
        assert_eq!(
            cache.authorize(Some(&token(now + 3600))).unwrap_err(),
            AuthError::ApprovalPending
        );
    }

    #[test]
    fn disabled_user_reason_has_precedence_over_revoked_session() {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let cache = cache();
        cache
            .upsert_user(
                UserState {
                    id: "user-1".into(),
                    username: "alice".into(),
                    role: "user".into(),
                    enabled: false,
                    approval_status: "approved".into(),
                    token_version: 4,
                },
                "api-run-1",
                2,
            )
            .unwrap();
        cache
            .revoke_user_sessions("user-1", "api-run-1", 3)
            .unwrap();
        assert_eq!(
            cache.authorize(Some(&token(now + 3600))).unwrap_err(),
            AuthError::UserDisabled
        );
    }

    #[test]
    fn accepts_low_revision_after_api_restart() {
        let cache = cache();
        cache
            .revoke_user_sessions("user-1", "api-run-1", 50)
            .unwrap();
        cache
            .upsert_session(
                SessionState {
                    id: "session-1".into(),
                    user_id: "user-1".into(),
                    expires_at: "2099-01-01T00:00:00Z".into(),
                    revoked_at: None,
                    client_device_id: "controller-1".into(),
                },
                "api-run-2",
                1,
            )
            .unwrap();

        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        assert!(cache.authorize(Some(&token(now + 3600))).is_ok());
    }

    #[test]
    fn acl_is_enforced_before_connection() {
        let cache = cache();
        cache
            .upsert_acl(
                AclRuleState {
                    id: "acl-1".into(),
                    subject_type: "user".into(),
                    subject_id: "user-1".into(),
                    target_type: "device".into(),
                    target_id: "target-1".into(),
                    permissions: vec!["file_transfer".into()],
                    effect: "allow".into(),
                    enabled: true,
                    priority: 100,
                    deleted: false,
                },
                "api-run-1",
                2,
            )
            .unwrap();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let decision = cache.authorize(Some(&token(now + 3600))).unwrap().unwrap();
        assert_eq!(
            cache.authorize_policy(&decision, "target-1", 0),
            Err(AuthError::AccessDenied)
        );
        assert_eq!(cache.authorize_policy(&decision, "target-1", 1), Ok(false));
        assert_eq!(
            cache.authorize_policy(&decision, "target-2", 1),
            Err(AuthError::AccessDenied)
        );
    }

    #[test]
    fn acl_priority_and_explicit_deny_are_deterministic() {
        let cache = cache();
        for (id, effect, priority, revision) in
            [("allow", "allow", 100, 2), ("deny", "deny", 100, 3)]
        {
            cache
                .upsert_acl(
                    AclRuleState {
                        id: id.into(),
                        subject_type: "user".into(),
                        subject_id: "user-1".into(),
                        target_type: "device".into(),
                        target_id: "target-1".into(),
                        permissions: vec!["remote_control".into()],
                        effect: effect.into(),
                        enabled: true,
                        priority,
                        deleted: false,
                    },
                    "api-run-1",
                    revision,
                )
                .unwrap();
        }
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let decision = cache.authorize(Some(&token(now + 3600))).unwrap().unwrap();
        assert_eq!(
            cache.authorize_policy(&decision, "target-1", 0),
            Err(AuthError::AccessDenied)
        );
    }

    #[test]
    fn user_group_membership_drives_acl_and_strategy() {
        let cache = cache();
        cache
            .upsert_user_group_membership(
                UserGroupMembershipState {
                    group_id: "operators".into(),
                    user_id: "user-1".into(),
                    active: true,
                },
                "api-run-1",
                2,
            )
            .unwrap();
        cache
            .upsert_acl(
                AclRuleState {
                    id: "group-acl".into(),
                    subject_type: "user_group".into(),
                    subject_id: "operators".into(),
                    target_type: "device".into(),
                    target_id: "target-1".into(),
                    permissions: vec!["remote_control".into()],
                    effect: "allow".into(),
                    enabled: true,
                    priority: 100,
                    deleted: false,
                },
                "api-run-1",
                3,
            )
            .unwrap();
        cache
            .upsert_strategy(
                StrategyState {
                    id: "group-strategy".into(),
                    scope_type: "user_group".into(),
                    scope_id: "operators".into(),
                    priority: 10,
                    settings: serde_json::from_value(serde_json::json!({"force_relay": true}))
                        .unwrap(),
                    enabled: true,
                    deleted: false,
                },
                "api-run-1",
                4,
            )
            .unwrap();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let decision = cache.authorize(Some(&token(now + 3600))).unwrap().unwrap();
        assert_eq!(cache.authorize_policy(&decision, "target-1", 0), Ok(true));
        cache
            .upsert_user_group_membership(
                UserGroupMembershipState {
                    group_id: "operators".into(),
                    user_id: "user-1".into(),
                    active: false,
                },
                "api-run-1",
                5,
            )
            .unwrap();
        assert_eq!(
            cache.authorize_policy(&decision, "target-1", 0),
            Err(AuthError::AccessDenied)
        );
    }

    #[test]
    fn strategy_can_deny_or_force_relay() {
        let cache = cache();
        cache
            .upsert_strategy(
                StrategyState {
                    id: "strategy-1".into(),
                    scope_type: "global".into(),
                    scope_id: String::new(),
                    priority: 100,
                    settings: serde_json::from_value(
                        serde_json::json!({"allow_remote_control": false, "force_relay": true}),
                    )
                    .unwrap(),
                    enabled: true,
                    deleted: false,
                },
                "api-run-1",
                2,
            )
            .unwrap();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let decision = cache.authorize(Some(&token(now + 3600))).unwrap().unwrap();
        assert_eq!(
            cache.authorize_policy(&decision, "target-1", 0),
            Err(AuthError::StrategyDenied)
        );
        assert_eq!(cache.authorize_policy(&decision, "target-1", 1), Ok(true));
        cache
            .upsert_strategy(
                StrategyState {
                    id: "strategy-override".into(),
                    scope_type: "global".into(),
                    scope_id: String::new(),
                    priority: 200,
                    settings: serde_json::from_value(
                        serde_json::json!({"allow_remote_control": true, "force_relay": false}),
                    )
                    .unwrap(),
                    enabled: true,
                    deleted: false,
                },
                "api-run-1",
                3,
            )
            .unwrap();
        assert_eq!(cache.authorize_policy(&decision, "target-1", 0), Ok(false));
    }

    #[test]
    fn policy_tombstones_remove_cached_rules_immediately() {
        let cache = cache();
        cache
            .upsert_acl(
                AclRuleState {
                    id: "acl-delete".into(),
                    subject_type: "user".into(),
                    subject_id: "user-1".into(),
                    target_type: "device".into(),
                    target_id: "target-1".into(),
                    permissions: vec!["remote_control".into()],
                    effect: "allow".into(),
                    enabled: true,
                    priority: 100,
                    deleted: false,
                },
                "api-run-1",
                2,
            )
            .unwrap();
        cache
            .upsert_acl(
                AclRuleState {
                    id: "acl-delete".into(),
                    subject_type: String::new(),
                    subject_id: String::new(),
                    target_type: String::new(),
                    target_id: String::new(),
                    permissions: Vec::new(),
                    effect: "allow".into(),
                    enabled: false,
                    priority: 0,
                    deleted: true,
                },
                "api-run-1",
                3,
            )
            .unwrap();

        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let decision = cache.authorize(Some(&token(now + 3600))).unwrap().unwrap();
        assert_eq!(cache.authorize_policy(&decision, "target-1", 0), Ok(false));
    }

    #[test]
    fn relay_selection_uses_healthy_low_load_cache_and_tombstones() {
        let cache = cache();
        for (index, relay) in [
            RelayState {
                id: "offline".into(),
                hostname: "offline.example".into(),
                port: 21117,
                region: String::new(),
                enabled: true,
                health: "offline".into(),
                latency_ms: 1,
                connections: 0,
                deleted: false,
            },
            RelayState {
                id: "busy".into(),
                hostname: "busy.example".into(),
                port: 21117,
                region: String::new(),
                enabled: true,
                health: "healthy".into(),
                latency_ms: 1,
                connections: 8,
                deleted: false,
            },
            RelayState {
                id: "best".into(),
                hostname: "best.example".into(),
                port: 21117,
                region: String::new(),
                enabled: true,
                health: "healthy".into(),
                latency_ms: 20,
                connections: 1,
                deleted: false,
            },
        ]
        .into_iter()
        .enumerate()
        {
            cache
                .upsert_relay(relay, "api-run-1", index as i64 + 2)
                .unwrap();
        }
        assert_eq!(cache.select_relay().as_deref(), Some("best.example:21117"));
        cache
            .upsert_relay(
                RelayState {
                    id: "best".into(),
                    hostname: String::new(),
                    port: 0,
                    region: String::new(),
                    enabled: false,
                    health: String::new(),
                    latency_ms: 0,
                    connections: 0,
                    deleted: true,
                },
                "api-run-1",
                5,
            )
            .unwrap();
        assert_eq!(cache.select_relay().as_deref(), Some("busy.example:21117"));
    }
}
