use std::{path::PathBuf, sync::Arc, time::Duration};

use anyhow::Context;
use futures_util::StreamExt;
use serde::Deserialize;

use crate::auth::{AuthCache, AuthSnapshot};

#[derive(Clone)]
pub struct AuthSyncClient {
    client: reqwest::Client,
    api_base: String,
    internal_token: String,
    cache_file: PathBuf,
    reconciliation_interval: Duration,
}

#[derive(Deserialize)]
struct EventEnvelope {
    source_id: String,
    revision: i64,
    #[serde(rename = "type")]
    kind: String,
    payload: serde_json::Value,
}

impl AuthSyncClient {
    pub fn new(
        api_base: String,
        internal_token: String,
        cache_file: PathBuf,
        reconciliation_interval: Duration,
    ) -> anyhow::Result<Self> {
        let client = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(5))
            .timeout(Duration::from_secs(30))
            .build()?;
        Ok(Self {
            client,
            api_base: api_base.trim_end_matches('/').to_owned(),
            internal_token,
            cache_file,
            reconciliation_interval,
        })
    }

    pub async fn reconcile(&self, cache: &AuthCache) -> anyhow::Result<()> {
        let snapshot = self
            .client
            .get(format!("{}/internal/v1/auth/snapshot", self.api_base))
            .header("X-RDS-Internal-Token", &self.internal_token)
            .send()
            .await?
            .error_for_status()?
            .json::<AuthSnapshot>()
            .await?;
        cache.replace(snapshot)?;
        cache.save(&self.cache_file)?;
        Ok(())
    }

    pub async fn run_reconciliation(self, cache: Arc<AuthCache>) {
        let mut interval = tokio::time::interval(self.reconciliation_interval);
        loop {
            interval.tick().await;
            if let Err(error) = self.reconcile(&cache).await {
                tracing::warn!(%error, "auth reconciliation failed; retaining last valid cache");
            }
        }
    }

    pub async fn run_events(self, cache: Arc<AuthCache>) {
        loop {
            if let Err(error) = self.consume_event_stream(&cache).await {
                tracing::warn!(%error, "auth event stream disconnected");
            }
            tokio::time::sleep(Duration::from_secs(2)).await;
        }
    }

    async fn consume_event_stream(&self, cache: &AuthCache) -> anyhow::Result<()> {
        let (source_id, revision) = cache.cursor();
        let response = self
            .client
            .get(format!("{}/internal/v1/auth/events", self.api_base))
            .query(&[("source_id", source_id), ("after", revision.to_string())])
            .header("X-RDS-Internal-Token", &self.internal_token)
            .send()
            .await?
            .error_for_status()?;
        let mut stream = response.bytes_stream();
        let mut pending = String::new();
        while let Some(chunk) = stream.next().await {
            pending.push_str(std::str::from_utf8(&chunk?).context("event stream is not UTF-8")?);
            while let Some(boundary) = pending.find("\n\n") {
                let frame = pending[..boundary].to_owned();
                pending.drain(..boundary + 2);
                if let Some(data) = frame.lines().find_map(|line| line.strip_prefix("data: ")) {
                    self.apply_event(cache, data)?;
                    cache.save(&self.cache_file)?;
                }
            }
        }
        anyhow::bail!("event stream ended")
    }

    fn apply_event(&self, cache: &AuthCache, data: &str) -> anyhow::Result<()> {
        let event: EventEnvelope = serde_json::from_str(data)?;
        match event.kind.as_str() {
            "USER_UPDATED" | "USER_DISABLED" => cache.upsert_user(
                serde_json::from_value(event.payload)?,
                &event.source_id,
                event.revision,
            ),
            "SESSION_CREATED" | "SESSION_REVOKED" => cache.upsert_session(
                serde_json::from_value(event.payload)?,
                &event.source_id,
                event.revision,
            ),
            "SESSION_REVOKED_ALL" => {
                #[derive(Deserialize)]
                struct UserReference {
                    user_id: String,
                }
                let reference: UserReference = serde_json::from_value(event.payload)?;
                cache.revoke_user_sessions(&reference.user_id, &event.source_id, event.revision)
            }
            "ACL_UPDATED" => cache.upsert_acl(
                serde_json::from_value(event.payload)?,
                &event.source_id,
                event.revision,
            ),
            "STRATEGY_UPDATED" => cache.upsert_strategy(
                serde_json::from_value(event.payload)?,
                &event.source_id,
                event.revision,
            ),
            "RELAY_UPDATED" => cache.upsert_relay(
                serde_json::from_value(event.payload)?,
                &event.source_id,
                event.revision,
            ),
            "DEVICE_UPDATED" => cache.upsert_device(
                serde_json::from_value(event.payload)?,
                &event.source_id,
                event.revision,
            ),
            "USER_GROUP_MEMBERSHIP_UPDATED" => cache.upsert_user_group_membership(
                serde_json::from_value(event.payload)?,
                &event.source_id,
                event.revision,
            ),
            "CONFIGURATION_UPDATED" => {
                #[derive(Deserialize)]
                struct Configuration {
                    require_login: bool,
                    require_device_deployment: bool,
                }
                let value: Configuration = serde_json::from_value(event.payload)?;
                cache.apply_configuration(
                    value.require_login,
                    value.require_device_deployment,
                    &event.source_id,
                    event.revision,
                )
            }
            _ => Ok(()),
        }
    }
}

pub fn load_last_valid_cache(cache: &AuthCache, path: &std::path::Path) {
    match cache.load(path) {
        Ok(()) => tracing::info!(revision = cache.revision(), "loaded persisted auth cache"),
        Err(error)
            if error
                .downcast_ref::<std::io::Error>()
                .is_some_and(|value| value.kind() == std::io::ErrorKind::NotFound) => {}
        Err(error) => tracing::warn!(%error, "could not load persisted auth cache"),
    }
}
