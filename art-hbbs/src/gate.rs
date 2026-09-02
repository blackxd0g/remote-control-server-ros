use std::sync::Arc;

use art_core::{
    audit::{AuditSender, ConnectionAudit},
    auth::AuthCache,
    protocol::{PunchHoleRequest, RequestRelay},
};

pub struct ConnectionGate {
    cache: Arc<AuthCache>,
    audit: AuditSender,
    require_device_deployment: bool,
}

#[derive(Debug)]
pub struct GateDecision {
    pub force_relay: bool,
    pub user_id: String,
    pub session_id: String,
    pub controller_device_id: String,
}

impl ConnectionGate {
    pub fn new(cache: Arc<AuthCache>, audit: AuditSender, require_device_deployment: bool) -> Self {
        Self {
            cache,
            audit,
            require_device_deployment,
        }
    }

    pub fn device_registration_allowed(&self, id: &str, public_key: &str) -> bool {
        !self
            .cache
            .require_device_deployment(self.require_device_deployment)
            || self.cache.device_registration_allowed(id, public_key)
    }

    pub fn authorize_punch(
        &self,
        request: &PunchHoleRequest,
        peer_ip: &str,
    ) -> Result<GateDecision, String> {
        self.authorize(
            &request.token,
            &request.id,
            request.conn_type,
            peer_ip,
            "punch_hole",
        )
    }

    pub fn authorize_relay(
        &self,
        request: &RequestRelay,
        peer_ip: &str,
    ) -> Result<GateDecision, String> {
        self.authorize(
            &request.token,
            &request.id,
            request.conn_type,
            peer_ip,
            "relay_request",
        )
    }

    pub fn select_relay(&self, fallback: &str) -> String {
        self.cache
            .select_relay()
            .unwrap_or_else(|| fallback.to_owned())
    }

    pub fn record_relay_assignment(
        &self,
        decision: &GateDecision,
        target: &str,
        peer_ip: &str,
        connection_type: i32,
        relay_uuid: &str,
        relay_server: &str,
    ) {
        self.audit.try_record(ConnectionAudit::new(
            "connection_relay_assigned",
            decision.user_id.clone(),
            decision.session_id.clone(),
            decision.controller_device_id.clone(),
            target.to_owned(),
            peer_ip.to_owned(),
            "allowed",
            String::new(),
            serde_json::json!({
                "connection_type": connection_type,
                "request_kind": "relay_request",
                "authorization_stage": "relay_assigned",
                "relay_uuid": relay_uuid,
                "relay_server": relay_server,
                "transport": "relay"
            }),
        ));
    }

    fn authorize(
        &self,
        token: &str,
        target: &str,
        connection_type: i32,
        peer_ip: &str,
        request_kind: &'static str,
    ) -> Result<GateDecision, String> {
        match self.cache.authorize(Some(token)) {
            Ok(Some(decision)) => {
                let force_relay = match self.cache.authorize_policy(
                    &decision,
                    target,
                    connection_type,
                ) {
                    Ok(value) => value,
                    Err(error) => {
                        let reason = error.to_string();
                        self.audit.try_record(ConnectionAudit::new("connection_denied", decision.user_id.clone(), decision.session_id.clone(), decision.controller_device_id.clone(), target.to_owned(), peer_ip.to_owned(), "denied", reason.clone(), serde_json::json!({"connection_type": connection_type, "request_kind": request_kind, "authorization_stage": "policy"})));
                        return Err(reason);
                    }
                };
                self.audit.try_record(ConnectionAudit::new("connection_allowed", decision.user_id.clone(), decision.session_id.clone(), decision.controller_device_id.clone(), target.to_owned(), peer_ip.to_owned(), "allowed", String::new(), serde_json::json!({"connection_type": connection_type, "request_kind": request_kind, "authorization_stage": "pre_rendezvous"})));
                Ok(GateDecision {
                    force_relay,
                    user_id: decision.user_id,
                    session_id: decision.session_id,
                    controller_device_id: decision.controller_device_id,
                })
            }
            Ok(None) => {
                self.audit.try_record(ConnectionAudit::new("connection_allowed", String::new(), String::new(), String::new(), target.to_owned(), peer_ip.to_owned(), "allowed", String::new(), serde_json::json!({"connection_type": connection_type, "request_kind": request_kind, "authorization_stage": "pre_rendezvous", "anonymous": true})));
                Ok(GateDecision {
                    force_relay: false,
                    user_id: String::new(),
                    session_id: String::new(),
                    controller_device_id: String::new(),
                })
            }
            Err(error) => {
                let reason = error.to_string();
                self.audit.try_record(ConnectionAudit::new("connection_denied", String::new(), String::new(), String::new(), target.to_owned(), peer_ip.to_owned(), "denied", reason.clone(), serde_json::json!({"connection_type": connection_type, "request_kind": request_kind, "authorization_stage": "pre_rendezvous"})));
                Err(reason)
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use art_core::{audit::AuditSender, auth::JwtVerifier};

    #[test]
    fn anonymous_punch_is_denied_before_target_processing() {
        let cache = Arc::new(AuthCache::new(
            JwtVerifier::hs256(
                b"0123456789abcdef0123456789abcdef",
                "art-rustdesk",
                "art-hbbs",
            )
            .unwrap(),
            true,
        ));
        let gate = ConnectionGate::new(cache, AuditSender::discard(), false);
        let request = PunchHoleRequest {
            id: "nonexistent-target".into(),
            ..Default::default()
        };
        assert_eq!(
            gate.authorize_punch(&request, "192.0.2.1").unwrap_err(),
            "Для подключения необходимо войти в аккаунт RustDesk"
        );
    }
}
