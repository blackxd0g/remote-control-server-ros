use std::{env, sync::Arc, time::Duration};

use serde::{Deserialize, Serialize};

use crate::server::RendezvousServer;
use art_core::{auth::AuthCache, sync::AuthSyncClient};

fn env_value(name: &str) -> Result<String, env::VarError> {
    let rds_name = name
        .strip_prefix("ART_")
        .map(|suffix| format!("RDS_{suffix}"));
    rds_name
        .and_then(|key| env::var(key).ok())
        .map_or_else(|| env::var(name), Ok)
}

#[derive(Serialize)]
struct Heartbeat<'a> {
    service: &'static str,
    instance_id: &'a str,
    online_peers: usize,
    acknowledged_commands: &'a [String],
}

#[derive(Deserialize)]
struct HeartbeatResponse {
    #[serde(default)]
    commands: Vec<ServiceCommand>,
}

#[derive(Deserialize)]
struct ServiceCommand {
    id: String,
    #[serde(rename = "type")]
    kind: String,
}

pub async fn run(
    api_base: String,
    internal_token: String,
    server: Arc<RendezvousServer>,
    sync: AuthSyncClient,
    cache: Arc<AuthCache>,
) {
    let instance_id = env_value("ART_HBBS_ID").unwrap_or_else(|_| "builtin-hbbs".into());
    let seconds = env_value("ART_SERVICE_HEARTBEAT_INTERVAL")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(5)
        .clamp(2, 60);
    let client = match reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(3))
        .timeout(Duration::from_secs(5))
        .build()
    {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, "service heartbeat client unavailable");
            return;
        }
    };
    let endpoint = format!(
        "{}/internal/v1/services/heartbeat",
        api_base.trim_end_matches('/')
    );
    let mut interval = tokio::time::interval(Duration::from_secs(seconds));
    let mut acknowledged = Vec::<String>::new();
    loop {
        interval.tick().await;
        let heartbeat = Heartbeat {
            service: "hbbs",
            instance_id: &instance_id,
            online_peers: server.online_peers(),
            acknowledged_commands: &acknowledged,
        };
        match client
            .post(&endpoint)
            .header("X-RDS-Internal-Token", &internal_token)
            .json(&heartbeat)
            .send()
            .await
        {
            Ok(response) if response.status().is_success() => {
                acknowledged.clear();
                match response.json::<HeartbeatResponse>().await {
                    Ok(value) => {
                        for command in value.commands {
                            if command.kind == "reconcile_auth" {
                                match sync.reconcile(&cache).await {
                                    Ok(()) => {
                                        tracing::info!(command_id=%command.id, "server control reconciliation completed");
                                        acknowledged.push(command.id);
                                    }
                                    Err(error) => {
                                        tracing::warn!(command_id=%command.id, %error, "server control reconciliation failed")
                                    }
                                }
                            }
                        }
                    }
                    Err(error) => tracing::warn!(%error, "invalid service heartbeat response"),
                }
            }
            Ok(response) => {
                tracing::warn!(status = %response.status(), "service heartbeat rejected")
            }
            Err(error) => tracing::warn!(%error, "service heartbeat delivery failed"),
        }
    }
}
