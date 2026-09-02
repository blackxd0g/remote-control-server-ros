use std::{env, path::PathBuf, time::Duration};

fn env_value(name: &str) -> Result<String, env::VarError> {
    let rds_name = name
        .strip_prefix("ART_")
        .map(|suffix| format!("RDS_{suffix}"));
    rds_name
        .and_then(|key| env::var(key).ok())
        .map_or_else(|| env::var(name), Ok)
}

pub struct Config {
    pub tcp_listen: String,
    pub udp_listen: String,
    pub nat_listen: String,
    pub websocket_listen: String,
    pub api_base: String,
    pub jwt_secret_file: PathBuf,
    pub internal_secret_file: PathBuf,
    pub auth_cache_file: PathBuf,
    pub require_login: bool,
    pub require_device_deployment: bool,
    pub relay_server: String,
    pub reconcile_interval: Duration,
    pub hbbr_control_address: String,
    pub server_key_file: PathBuf,
}

impl Config {
    pub fn load() -> anyhow::Result<Self> {
        let data_dir = env_value("ART_DATA_DIR").unwrap_or_else(|_| "/data".into());
        Ok(Self {
            tcp_listen: env_value("ART_HBBS_TCP_LISTEN").unwrap_or_else(|_| "0.0.0.0:21116".into()),
            udp_listen: env_value("ART_HBBS_UDP_LISTEN").unwrap_or_else(|_| "0.0.0.0:21116".into()),
            nat_listen: env_value("ART_HBBS_NAT_LISTEN").unwrap_or_else(|_| "0.0.0.0:21115".into()),
            websocket_listen: env_value("ART_HBBS_WEBSOCKET_LISTEN")
                .unwrap_or_else(|_| "0.0.0.0:21118".into()),
            api_base: env_value("ART_API_INTERNAL_URL")
                .unwrap_or_else(|_| "http://127.0.0.1:21114".into()),
            jwt_secret_file: env_value("ART_JWT_SECRET_FILE")
                .map(PathBuf::from)
                .unwrap_or_else(|_| PathBuf::from(&data_dir).join("secrets/jwt.secret")),
            internal_secret_file: env_value("ART_INTERNAL_SECRET_FILE")
                .map(PathBuf::from)
                .unwrap_or_else(|_| PathBuf::from(&data_dir).join("secrets/internal.secret")),
            auth_cache_file: env_value("ART_AUTH_CACHE_FILE")
                .map(PathBuf::from)
                .unwrap_or_else(|_| PathBuf::from(&data_dir).join("cache/auth.json")),
            require_login: env_value("ART_REQUIRE_LOGIN").map_or(true, |value| {
                !matches!(value.as_str(), "0" | "false" | "off")
            }),
            require_device_deployment: env_value("ART_REQUIRE_DEVICE_DEPLOYMENT")
                .is_ok_and(|value| matches!(value.as_str(), "1" | "true" | "on")),
            relay_server: env_value("ART_RELAY_SERVER")
                .unwrap_or_else(|_| "127.0.0.1:21117".into()),
            reconcile_interval: Duration::from_secs(
                env_value("ART_AUTH_RECONCILE_SECONDS")
                    .ok()
                    .and_then(|value| value.parse().ok())
                    .unwrap_or(60),
            ),
            hbbr_control_address: env_value("ART_HBBR_CONTROL_ADDRESS")
                .unwrap_or_else(|_| "127.0.0.1:21119".into()),
            server_key_file: env_value("ART_SERVER_KEY_FILE")
                .map(PathBuf::from)
                .unwrap_or_else(|_| PathBuf::from(&data_dir).join("secrets/id_ed25519")),
        })
    }
}
