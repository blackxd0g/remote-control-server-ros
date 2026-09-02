use std::{env, sync::Arc, time::Duration};

use serde::Serialize;
use tokio::sync::mpsc;

use crate::metrics::RelayMetrics;

fn env_value(name: &str) -> Result<String, env::VarError> {
    let rds_name = name
        .strip_prefix("ART_")
        .map(|suffix| format!("RDS_{suffix}"));
    rds_name
        .and_then(|key| env::var(key).ok())
        .map_or_else(|| env::var(name), Ok)
}

pub struct TelemetryConfig {
    api_url: String,
    internal_secret: String,
    relay_id: String,
    relay_name: String,
    hostname: String,
    port: u16,
    region: String,
    interval: Duration,
}

#[derive(Clone)]
pub struct LifecycleReporter {
    sender: mpsc::Sender<LifecycleReport>,
}

#[derive(Clone, Serialize)]
struct LifecycleReport {
    uuid: String,
    state: &'static str,
}

impl Default for LifecycleReporter {
    fn default() -> Self {
        let (sender, receiver) = mpsc::channel(1);
        drop(receiver);
        Self { sender }
    }
}

impl LifecycleReporter {
    pub fn try_report(&self, uuid: &str, state: &'static str) {
        if self
            .sender
            .try_send(LifecycleReport {
                uuid: uuid.to_owned(),
                state,
            })
            .is_err()
        {
            tracing::debug!(%uuid, %state, "relay lifecycle queue unavailable");
        }
    }
}

#[derive(Serialize)]
struct TelemetryReport<'a> {
    id: &'a str,
    name: &'a str,
    hostname: &'a str,
    port: u16,
    region: &'a str,
    connections: usize,
    bandwidth: u64,
}

impl TelemetryConfig {
    pub fn from_env(internal_secret: String) -> anyhow::Result<Self> {
        let address = env_value("ART_HBBR_PUBLIC_ADDRESS")
            .or_else(|_| env_value("ART_RELAY_SERVER"))
            .unwrap_or_else(|_| "127.0.0.1:21117".into());
        let (hostname, port) = split_address(&address)?;
        let relay_id = env_value("ART_HBBR_ID").unwrap_or_else(|_| "builtin-hbbr".into());
        let interval = env_value("ART_HBBR_TELEMETRY_INTERVAL")
            .ok()
            .and_then(|value| value.parse::<u64>().ok())
            .map(|seconds| Duration::from_secs(seconds.clamp(2, 300)))
            .unwrap_or(Duration::from_secs(5));
        Ok(Self {
            api_url: env_value("ART_API_INTERNAL_URL")
                .unwrap_or_else(|_| "http://127.0.0.1:21114".into()),
            internal_secret,
            relay_id,
            relay_name: env_value("ART_HBBR_NAME").unwrap_or_else(|_| "Built-in HBBR".into()),
            hostname,
            port,
            region: env_value("ART_HBBR_REGION").unwrap_or_default(),
            interval,
        })
    }

    pub fn start_lifecycle_reporter(&self) -> LifecycleReporter {
        let (sender, mut receiver) = mpsc::channel::<LifecycleReport>(1024);
        let endpoint = format!(
            "{}/internal/v1/relay/connections/state",
            self.api_url.trim_end_matches('/')
        );
        let secret = self.internal_secret.clone();
        tokio::spawn(async move {
            let client = reqwest::Client::new();
            while let Some(report) = receiver.recv().await {
                if let Err(error) = client
                    .post(&endpoint)
                    .header("X-RDS-Internal-Token", &secret)
                    .json(&report)
                    .send()
                    .await
                    .and_then(reqwest::Response::error_for_status)
                {
                    tracing::warn!(%error, uuid = %report.uuid, state = %report.state, "relay lifecycle delivery failed");
                }
            }
        });
        LifecycleReporter { sender }
    }
}

pub async fn run(config: TelemetryConfig, metrics: Arc<RelayMetrics>) {
    let client = match reqwest::Client::builder()
        .connect_timeout(Duration::from_secs(3))
        .timeout(Duration::from_secs(5))
        .build()
    {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, "relay telemetry client unavailable");
            return;
        }
    };
    let endpoint = format!(
        "{}/internal/v1/relay/telemetry",
        config.api_url.trim_end_matches('/')
    );
    let mut previous_bytes = metrics.total_bytes();
    let mut previous_at = tokio::time::Instant::now();
    let mut interval = tokio::time::interval(config.interval);
    loop {
        interval.tick().await;
        let now = tokio::time::Instant::now();
        let total_bytes = metrics.total_bytes();
        let elapsed = now.duration_since(previous_at).as_secs_f64().max(0.001);
        let bandwidth = ((total_bytes.saturating_sub(previous_bytes)) as f64 / elapsed) as u64;
        previous_bytes = total_bytes;
        previous_at = now;
        let report = TelemetryReport {
            id: &config.relay_id,
            name: &config.relay_name,
            hostname: &config.hostname,
            port: config.port,
            region: &config.region,
            connections: metrics.connections(),
            bandwidth,
        };
        match client
            .post(&endpoint)
            .header("X-RDS-Internal-Token", &config.internal_secret)
            .json(&report)
            .send()
            .await
        {
            Ok(response) if response.status().is_success() => {}
            Ok(response) => tracing::warn!(status = %response.status(), "relay telemetry rejected"),
            Err(error) => tracing::warn!(%error, "relay telemetry delivery failed"),
        }
    }
}

fn split_address(address: &str) -> anyhow::Result<(String, u16)> {
    let address = address.trim();
    let (hostname, port) = if let Some(rest) = address.strip_prefix('[') {
        let (hostname, port) = rest
            .split_once("]:")
            .ok_or_else(|| anyhow::anyhow!("invalid bracketed relay address"))?;
        (hostname, port)
    } else {
        address
            .rsplit_once(':')
            .ok_or_else(|| anyhow::anyhow!("relay address must include a port"))?
    };
    anyhow::ensure!(!hostname.is_empty(), "relay hostname is empty");
    Ok((hostname.to_owned(), port.parse()?))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_dns_and_ipv6_addresses() {
        assert_eq!(
            split_address("relay.example:21117").unwrap(),
            ("relay.example".into(), 21117)
        );
        assert_eq!(
            split_address("[2001:db8::1]:21117").unwrap(),
            ("2001:db8::1".into(), 21117)
        );
        assert!(split_address("missing-port").is_err());
    }
}
