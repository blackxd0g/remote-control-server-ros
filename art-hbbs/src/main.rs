mod config;
mod gate;
mod heartbeat;
mod registry;
mod server;

use std::{fs, sync::Arc};

use art_core::{
    audit::start_reporter,
    auth::{AuthCache, JwtVerifier},
    sync::{AuthSyncClient, load_last_valid_cache},
    transport::ServerKey,
};
use config::Config;
use gate::ConnectionGate;
use server::{RendezvousOptions, RendezvousServer};

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let config = Config::load()?;
    let jwt_secret = read_secret(&config.jwt_secret_file)?;
    let internal_token = read_secret(&config.internal_secret_file)?;
    let server_key = Arc::new(ServerKey::load_or_create(&config.server_key_file)?);
    tracing::info!(public_key = %server_key.public_key_base64(), "RustDesk server public key");
    let verifier = JwtVerifier::hs256(jwt_secret.as_bytes(), "art-rustdesk", "art-hbbs")?;
    let cache = Arc::new(AuthCache::new(verifier, config.require_login));
    load_last_valid_cache(&cache, &config.auth_cache_file);
    let sync = AuthSyncClient::new(
        config.api_base.clone(),
        internal_token.clone(),
        config.auth_cache_file.clone(),
        config.reconcile_interval,
    )?;
    if let Err(error) = sync.reconcile(&cache).await {
        tracing::warn!(%error, "initial auth reconciliation failed; using last valid cache");
    }
    tokio::spawn(sync.clone().run_events(cache.clone()));
    tokio::spawn(sync.clone().run_reconciliation(cache.clone()));
    let audit = start_reporter(config.api_base.clone(), internal_token.clone());
    let gate = Arc::new(ConnectionGate::new(
        cache.clone(),
        audit,
        config.require_device_deployment,
    ));
    let (server, listener) = RendezvousServer::bind(
        &config.tcp_listen,
        &config.udp_listen,
        gate,
        RendezvousOptions {
            relay_server: config.relay_server,
            relay_control_address: config.hbbr_control_address,
            internal_token: internal_token.clone(),
            api_base: config.api_base.clone(),
            server_key,
        },
    )
    .await?;
    tokio::spawn(heartbeat::run(
        config.api_base,
        internal_token,
        server.clone(),
        sync,
        cache,
    ));
    let nat_listener = tokio::net::TcpListener::bind(&config.nat_listen).await?;
    let websocket_listener = tokio::net::TcpListener::bind(&config.websocket_listen).await?;
    tracing::info!(tcp = %config.tcp_listen, udp = %config.udp_listen, nat = %config.nat_listen, websocket = %config.websocket_listen, "art-hbbs listening");
    server.run(listener, nat_listener, websocket_listener).await
}

fn read_secret(path: &std::path::Path) -> anyhow::Result<String> {
    let value = fs::read_to_string(path)?.trim().to_owned();
    anyhow::ensure!(
        value.len() >= 32,
        "secret in {} is too short",
        path.display()
    );
    Ok(value)
}
