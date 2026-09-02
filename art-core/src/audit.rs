use serde::Serialize;
use tokio::sync::mpsc;
use tokio::time::{Duration, sleep};
use uuid::Uuid;

#[derive(Clone, Debug, Serialize)]
pub struct ConnectionAudit {
    pub id: String,
    #[serde(rename = "type")]
    pub event_type: &'static str,
    pub actor_user_id: String,
    pub actor_session_id: String,
    pub controller_device_id: String,
    pub target_rustdesk_id: String,
    pub ip: String,
    pub result: &'static str,
    pub reason: String,
    pub metadata: serde_json::Value,
}

#[derive(Clone)]
pub struct AuditSender {
    sender: mpsc::Sender<ConnectionAudit>,
}

impl AuditSender {
    pub fn try_record(&self, event: ConnectionAudit) {
        if self.sender.try_send(event).is_err() {
            tracing::warn!("audit queue full; connection path remains non-blocking");
        }
    }

    pub fn discard() -> Self {
        let (sender, _receiver) = mpsc::channel(1);
        Self { sender }
    }
}

impl ConnectionAudit {
    #[expect(
        clippy::too_many_arguments,
        reason = "the constructor mirrors the flat immutable audit wire schema"
    )]
    pub fn new(
        event_type: &'static str,
        actor_user_id: String,
        actor_session_id: String,
        controller_device_id: String,
        target_rustdesk_id: String,
        ip: String,
        result: &'static str,
        reason: String,
        metadata: serde_json::Value,
    ) -> Self {
        Self {
            id: Uuid::new_v4().to_string(),
            event_type,
            actor_user_id,
            actor_session_id,
            controller_device_id,
            target_rustdesk_id,
            ip,
            result,
            reason,
            metadata,
        }
    }
}

pub fn start_reporter(api_base: String, internal_token: String) -> AuditSender {
    let (sender, mut receiver) = mpsc::channel::<ConnectionAudit>(1024);
    tokio::spawn(async move {
        let client = reqwest::Client::new();
        let endpoint = format!(
            "{}/internal/v1/audit/connections",
            api_base.trim_end_matches('/')
        );
        while let Some(event) = receiver.recv().await {
            let mut delay = Duration::from_millis(250);
            for attempt in 1..=5 {
                let result = client
                    .post(&endpoint)
                    .header("X-RDS-Internal-Token", &internal_token)
                    .json(&event)
                    .send()
                    .await
                    .and_then(reqwest::Response::error_for_status);
                match result {
                    Ok(_) => break,
                    Err(error) if attempt < 5 => {
                        tracing::warn!(%error, %attempt, event_id = %event.id, "connection audit delivery retry");
                        sleep(delay).await;
                        delay = delay.saturating_mul(2);
                    }
                    Err(error) => {
                        tracing::error!(%error, event_id = %event.id, "connection audit delivery failed after retries");
                    }
                }
            }
        }
    });
    AuditSender { sender }
}
