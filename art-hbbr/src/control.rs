use crate::{Permit, RelayState, env_value, terminate_relay};
use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use std::{
    collections::HashMap,
    fs,
    hash::BuildHasher,
    sync::Arc,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use tokio_tungstenite::{
    connect_async_with_config,
    tungstenite::{Message as WsMessage, client::IntoClientRequest, protocol::WebSocketConfig},
};

pub(crate) struct Config {
    url: String,
    credential: String,
}
#[derive(Deserialize)]
struct Credential {
    credential: String,
}
impl Config {
    pub(crate) fn from_env() -> anyhow::Result<Option<Self>> {
        let mode = env_value("ART_RELAY_CONTROL_MODE").unwrap_or_else(|_| "legacy".into());
        anyhow::ensure!(
            mode == "legacy" || mode == "wss",
            "invalid relay control mode"
        );
        if mode == "legacy" {
            return Ok(None);
        }
        let url = env_value("ART_RELAY_CONTROL_URL")?;
        let parsed = reqwest::Url::parse(&url)?;
        anyhow::ensure!(
            parsed.scheme() == "wss"
                && parsed.host_str().is_some()
                && parsed.username().is_empty()
                && parsed.password().is_none()
                && parsed.query().is_none()
                && parsed.fragment().is_none(),
            "relay control URL must use WSS without credentials/query"
        );
        let path = env_value("ART_RELAY_CREDENTIAL_FILE")?;
        let saved: Credential = serde_json::from_str(&fs::read_to_string(path)?)?;
        anyhow::ensure!(
            saved.credential.len() == 43,
            "invalid relay credential length"
        );
        Ok(Some(Self {
            url,
            credential: saved.credential,
        }))
    }
}

#[derive(Clone, Default, Deserialize, Serialize)]
struct Message {
    #[serde(default)]
    revision: String,
    #[serde(default)]
    offset: usize,
    #[serde(default)]
    quarantine_page: Option<crate::outbox::QuarantinePage>,
    #[serde(default)]
    delivery: Option<crate::outbox::DeliveryStatus>,
    #[serde(default)]
    reason: String,
    #[serde(default)]
    durable: bool,
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    session: String,
    #[serde(default)]
    request_id: String,
    #[serde(default)]
    uuid: String,
    #[serde(default)]
    action: String,
    #[serde(default)]
    status: String,
    #[serde(default)]
    expires_at: u64,
    #[serde(default)]
    connections: usize,
    #[serde(default)]
    bandwidth: u64,
}
fn now_millis() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

#[derive(Default)]
struct ReconnectBackoff {
    failures: u32,
}
impl ReconnectBackoff {
    fn next(&mut self, stable: bool, entropy: u64) -> Duration {
        if stable {
            self.failures = 0;
        }
        let ceiling = (1000_u64 << self.failures.min(5)).min(30_000);
        self.failures = self.failures.saturating_add(1);
        // Equal jitter avoids immediate retry loops and synchronised reconnect storms.
        Duration::from_millis(ceiling / 2 + entropy % (ceiling / 2 + 1))
    }
}

async fn apply_command(
    state: &RelayState,
    message: &Message,
    seen: &mut HashMap<String, (String, String, String)>,
) -> anyhow::Result<String> {
    anyhow::ensure!(
        message.request_id.len() <= 64
            && !message.request_id.is_empty()
            && (8..=128).contains(&message.uuid.len()),
        "invalid command identity"
    );
    if let Some((uuid, action, status)) = seen.get(&message.request_id) {
        anyhow::ensure!(
            uuid == &message.uuid && action == &message.action,
            "command replay mismatch"
        );
        return Ok(status.clone());
    }
    let now = now_millis();
    anyhow::ensure!(
        message.expires_at > now && message.expires_at <= now + 30_000,
        "expired or excessive command lifetime"
    );
    // Bound the deduplication cache; reconnect obtains a new server session epoch.
    anyhow::ensure!(seen.len() < 8192, "control epoch command limit reached");
    let status = match message.action.as_str() {
        "permit" => {
            anyhow::ensure!(state.lifecycle.available(), "outbox unavailable");
            anyhow::ensure!(
                !seen
                    .values()
                    .any(|(uuid, action, _)| uuid == &message.uuid && action == "permit"),
                "relay UUID cannot be reauthorized in the same epoch"
            );
            let _transition = state.transition.lock().await;
            let mut permits = state.permits.lock().await;
            anyhow::ensure!(permits.len() < 4096, "permit capacity reached");
            anyhow::ensure!(
                !permits.contains_key(&message.uuid)
                    && !state.pending.lock().await.contains_key(&message.uuid)
                    && !state.active.lock().await.contains_key(&message.uuid),
                "relay UUID already used"
            );
            permits.insert(
                message.uuid.clone(),
                Permit {
                    expires_at: Instant::now() + Duration::from_secs(30),
                    uses: 2,
                },
            );
            "permitted"
        }
        "terminate" => {
            if terminate_relay(state, &message.uuid).await {
                "terminated"
            } else {
                "not_found"
            }
        }
        _ => anyhow::bail!("unsupported control command"),
    }
    .to_owned();
    seen.insert(
        message.request_id.clone(),
        (message.uuid.clone(), message.action.clone(), status.clone()),
    );
    Ok(status)
}

pub(crate) async fn run(config: Config, state: Arc<RelayState>) {
    let mut backoff = ReconnectBackoff::default();
    loop {
        let started = Instant::now();
        if let Err(error) = session(&config, &state).await {
            // Never log the credential or request headers.
            tracing::warn!(%error,"secure relay control disconnected");
        }
        // Existing active transports survive. Unused authorizations cannot cross epochs.
        {
            let _transition = state.transition.lock().await;
            state.permits.lock().await.clear();
            state.pending.lock().await.clear();
        }
        let entropy = std::collections::hash_map::RandomState::new().hash_one(now_millis());
        let delay = backoff.next(started.elapsed() >= Duration::from_secs(60), entropy);
        tracing::info!(
            delay_ms = delay.as_millis() as u64,
            "relay control reconnect scheduled"
        );
        tokio::time::sleep(delay).await;
    }
}

async fn session(config: &Config, state: &Arc<RelayState>) -> anyhow::Result<()> {
    let mut request = config.url.as_str().into_client_request()?;
    request.headers_mut().insert(
        "Authorization",
        format!("Bearer {}", config.credential).parse()?,
    );
    let (mut ws, _) = tokio::time::timeout(
        Duration::from_secs(10),
        connect_async_with_config(
            request,
            Some(
                WebSocketConfig::default()
                    .max_message_size(Some(8192))
                    .max_frame_size(Some(8192)),
            ),
            false,
        ),
    )
    .await??;
    let first = tokio::time::timeout(Duration::from_secs(5), ws.next())
        .await?
        .ok_or_else(|| anyhow::anyhow!("missing control hello"))??;
    let hello: Message = serde_json::from_slice(&first.into_data())?;
    anyhow::ensure!(
        hello.kind == "hello" && !hello.session.is_empty() && hello.durable,
        "invalid control hello"
    );
    {
        let _transition = state.transition.lock().await;
        state.permits.lock().await.clear();
        state.pending.lock().await.clear();
    }

    tokio::time::timeout(
        Duration::from_secs(3),
        ws.send(WsMessage::Text(
            serde_json::to_string(&Message {
                kind: "ready".into(),
                session: hello.session.clone(),
                ..Default::default()
            })?
            .into(),
        )),
    )
    .await??;
    let mut seen = HashMap::new();
    let mut last_event: Option<(String, Instant)> = None;
    let mut storage_retry_after = Instant::now();
    let mut retry = tokio::time::interval(Duration::from_millis(20));
    retry.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    let mut tick = tokio::time::interval(Duration::from_secs(5));
    let mut last_read = Instant::now();
    let mut previous_bytes = state.metrics.total_bytes();
    let mut previous_at = Instant::now();
    loop {
        let outgoing = tokio::select! {
            incoming = ws.next() => {
                let incoming=incoming.ok_or_else(||anyhow::anyhow!("control closed"))??;
                last_read=Instant::now();
                match incoming {
                    WsMessage::Ping(bytes) => { tokio::time::timeout(Duration::from_secs(3), ws.send(WsMessage::Pong(bytes))).await??; continue; },
                    WsMessage::Pong(_) => continue,
                    WsMessage::Close(_) => anyhow::bail!("control closed"),
                    WsMessage::Text(text) => {
                        anyhow::ensure!(text.len() <= 8192,"control message too large");
                        let command: Message = serde_json::from_str(&text)?;
                        anyhow::ensure!(command.session == hello.session,"invalid control session");
                        if command.kind == "lifecycle_ack" {
                            state.lifecycle.acknowledge(command.request_id, command.uuid, command.status).await?;
                            continue;
                        }
                        if command.kind == "lifecycle_rejected" {
                            state.lifecycle.quarantine(command.request_id,command.uuid,command.status,command.reason).await?;
                            continue;
                        }
                        if command.kind == "quarantine" {
                            anyhow::ensure!(!command.request_id.is_empty() && command.request_id.len()<=64 && command.expires_at>now_millis() && command.expires_at<=now_millis()+30_000,"invalid quarantine request");
                            let result = state.lifecycle.manage_quarantine(command.action,command.revision,command.offset).await;
                            let response = match result {
                                Ok(page)=> Message{kind:"quarantine_reply".into(),session:hello.session.clone(),request_id:command.request_id,status:"ok".into(),quarantine_page:Some(page),..Default::default()},
                                Err(_)=> Message{kind:"quarantine_reply".into(),session:hello.session.clone(),request_id:command.request_id,status:"conflict".into(),..Default::default()},
                            };
                            tokio::time::timeout(Duration::from_secs(3),ws.send(WsMessage::Text(serde_json::to_string(&response)?.into()))).await??;
                            continue;
                        }
                        anyhow::ensure!(command.kind == "command", "invalid control command");
                        let status=apply_command(state,&command,&mut seen).await?;
                        Message{kind:"ack".into(),session:hello.session.clone(),request_id:command.request_id,uuid:command.uuid,status,..Default::default()}
                    },
                    _ => anyhow::bail!("invalid control frame"),
                }
            },
            _=retry.tick()=>{
                if Instant::now()<storage_retry_after {continue;}
                let report=match state.lifecycle.next_event().await {
                    Ok(Some(report))=>report,
                    Ok(None)=>continue,
                    Err(_)=>{storage_retry_after=Instant::now()+Duration::from_secs(5);continue;}
                };
                if last_event.as_ref().is_some_and(|(id, at)| id == &report.id && at.elapsed() < Duration::from_millis(750)) { continue; }
                last_event = Some((report.id.clone(),Instant::now()));
                Message{kind:"lifecycle".into(),session:hello.session.clone(),request_id:report.id,uuid:report.uuid,status:report.status,..Default::default()}
            },            _=tick.tick()=>{
                anyhow::ensure!(last_read.elapsed()<Duration::from_secs(35),"control heartbeat timeout");
                let bytes=state.metrics.total_bytes();let now=Instant::now();
                let bandwidth=((bytes.saturating_sub(previous_bytes)) as f64/now.duration_since(previous_at).as_secs_f64().max(0.001)) as u64;
                previous_bytes=bytes;previous_at=now;
                Message{kind:"telemetry".into(),session:hello.session.clone(),connections:state.metrics.connections(),bandwidth,delivery:Some(state.lifecycle.delivery_status().await?),..Default::default()}
            }
        };
        tokio::time::timeout(
            Duration::from_secs(3),
            ws.send(WsMessage::Text(serde_json::to_string(&outgoing)?.into())),
        )
        .await??;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn reconnect_delay_is_bounded_and_resets_only_after_stability() {
        let mut backoff = ReconnectBackoff::default();
        for ceiling in [1000, 2000, 4000, 8000, 16000, 30000, 30000] {
            let delay = backoff.next(false, u64::MAX).as_millis();
            assert!(delay >= ceiling / 2 && delay <= ceiling);
        }
        assert_eq!(backoff.next(true, 0), Duration::from_millis(500));
        assert_eq!(backoff.next(false, 0), Duration::from_millis(1000));
    }

    #[tokio::test]
    async fn permit_replay_does_not_restore_uses_and_expiry_is_enforced() {
        let state = RelayState::default();
        let mut seen = HashMap::new();
        let command = Message {
            kind: "command".into(),
            request_id: "request-1".into(),
            uuid: "relay-uuid-1".into(),
            action: "permit".into(),
            expires_at: now_millis() + 5000,
            ..Default::default()
        };
        assert_eq!(
            apply_command(&state, &command, &mut seen).await.unwrap(),
            "permitted"
        );
        state
            .permits
            .lock()
            .await
            .get_mut(&command.uuid)
            .unwrap()
            .uses = 1;
        apply_command(&state, &command, &mut seen).await.unwrap();
        assert_eq!(state.permits.lock().await[&command.uuid].uses, 1);
        let expired = Message {
            request_id: "request-2".into(),
            expires_at: now_millis() - 1,
            ..command.clone()
        };
        assert!(apply_command(&state, &expired, &mut seen).await.is_err());
        let foreign = Message {
            uuid: "another-uuid".into(),
            ..command
        };
        assert!(apply_command(&state, &foreign, &mut seen).await.is_err());
    }
}
