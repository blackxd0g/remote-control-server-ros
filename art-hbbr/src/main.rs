use std::{
    collections::HashMap,
    env, fs,
    sync::Arc,
    time::{Duration, Instant},
};

use art_core::protocol::{RendezvousMessage, rendezvous_message};
use bytes::Bytes;
use futures_util::{SinkExt, StreamExt};
use prost::Message;
use serde::Deserialize;
use subtle::ConstantTimeEq;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream, UdpSocket},
    sync::Mutex,
};
use tokio_tungstenite::{WebSocketStream, accept_async, tungstenite::Message as WebSocketMessage};

mod metrics;
mod telemetry;

fn env_value(name: &str) -> Result<String, env::VarError> {
    let rds_name = name
        .strip_prefix("ART_")
        .map(|suffix| format!("RDS_{suffix}"));
    rds_name
        .and_then(|key| env::var(key).ok())
        .map_or_else(|| env::var(name), Ok)
}

use metrics::RelayMetrics;

struct Pending {
    connection: RelayConnection,
    created_at: Instant,
}

enum RelayConnection {
    Tcp(TcpStream),
    WebSocket(Box<WebSocketStream<TcpStream>>),
}
struct Permit {
    expires_at: Instant,
    uses: u8,
}

#[derive(Default)]
struct RelayState {
    pending: Mutex<HashMap<String, Pending>>,
    permits: Mutex<HashMap<String, Permit>>,
    metrics: Arc<RelayMetrics>,
}

#[derive(Deserialize)]
struct PermitMessage {
    token: String,
    uuid: String,
    expires_in: u64,
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .init();
    let listen = env_value("ART_HBBR_LISTEN").unwrap_or_else(|_| "0.0.0.0:21117".into());
    let control = env_value("ART_HBBR_CONTROL_LISTEN").unwrap_or_else(|_| "0.0.0.0:21119".into());
    let data_dir = env_value("ART_DATA_DIR").unwrap_or_else(|_| "/data".into());
    let secret_path = env_value("ART_INTERNAL_SECRET_FILE")
        .unwrap_or_else(|_| format!("{data_dir}/secrets/internal.secret"));
    let secret = fs::read_to_string(&secret_path)?.trim().to_owned();
    anyhow::ensure!(secret.len() >= 32, "internal secret is too short");
    let metrics = Arc::new(RelayMetrics::default());
    let state = Arc::new(RelayState {
        metrics: metrics.clone(),
        ..RelayState::default()
    });
    let telemetry = telemetry::TelemetryConfig::from_env(secret.clone())?;
    tokio::spawn(telemetry::run(telemetry, metrics));
    let control_socket = UdpSocket::bind(&control).await?;
    tokio::spawn(run_control(control_socket, state.clone(), secret));
    tokio::spawn(cleanup(state.clone()));
    let listener = TcpListener::bind(&listen).await?;
    let websocket_listen =
        env_value("ART_HBBR_WEBSOCKET_LISTEN").unwrap_or_else(|_| "0.0.0.0:21119".into());
    let websocket_listener = TcpListener::bind(&websocket_listen).await?;
    tracing::info!(%listen, %control, %websocket_listen, "art-hbbr listening");
    let websocket_state = state.clone();
    tokio::spawn(async move {
        loop {
            let Ok((stream, address)) = websocket_listener.accept().await else {
                break;
            };
            let state = websocket_state.clone();
            tokio::spawn(async move {
                if let Err(error) = accept_websocket_relay(stream, state).await {
                    tracing::debug!(%address,%error,"WebSocket relay connection rejected");
                }
            });
        }
    });
    loop {
        let (stream, address) = listener.accept().await?;
        let state = state.clone();
        tokio::spawn(async move {
            if let Err(error) = accept_relay(stream, state).await {
                tracing::debug!(%address,%error,"relay connection rejected");
            }
        });
    }
}

async fn run_control(socket: UdpSocket, state: Arc<RelayState>, secret: String) {
    let mut buffer = vec![0u8; 4096];
    loop {
        let Ok((length, _address)) = socket.recv_from(&mut buffer).await else {
            continue;
        };
        let Ok(message) = serde_json::from_slice::<PermitMessage>(&buffer[..length]) else {
            continue;
        };
        if message
            .token
            .as_bytes()
            .ct_eq(secret.as_bytes())
            .unwrap_u8()
            != 1
            || message.uuid.is_empty()
        {
            continue;
        }
        let lifetime = Duration::from_secs(message.expires_in.clamp(1, 300));
        state.permits.lock().await.insert(
            message.uuid,
            Permit {
                expires_at: Instant::now() + lifetime,
                uses: 2,
            },
        );
    }
}

async fn accept_relay(mut stream: TcpStream, state: Arc<RelayState>) -> anyhow::Result<()> {
    let first = read_frame(&mut stream).await?;
    let message = RendezvousMessage::decode(first.as_slice())?;
    let uuid = match message.union {
        Some(rendezvous_message::Union::RequestRelay(request)) if !request.uuid.is_empty() => {
            request.uuid
        }
        _ => anyhow::bail!("first relay frame is not RequestRelay"),
    };
    accept_authorized(uuid, RelayConnection::Tcp(stream), state).await
}

async fn accept_websocket_relay(stream: TcpStream, state: Arc<RelayState>) -> anyhow::Result<()> {
    let mut websocket = accept_async(stream).await?;
    let first = websocket
        .next()
        .await
        .ok_or_else(|| anyhow::anyhow!("missing WebSocket relay request"))??;
    let WebSocketMessage::Binary(first) = first else {
        anyhow::bail!("first WebSocket relay frame is not binary")
    };
    let message = RendezvousMessage::decode(first)?;
    let uuid = match message.union {
        Some(rendezvous_message::Union::RequestRelay(request)) if !request.uuid.is_empty() => {
            request.uuid
        }
        _ => anyhow::bail!("first WebSocket relay frame is not RequestRelay"),
    };
    accept_authorized(uuid, RelayConnection::WebSocket(Box::new(websocket)), state).await
}

async fn accept_authorized(
    uuid: String,
    connection: RelayConnection,
    state: Arc<RelayState>,
) -> anyhow::Result<()> {
    {
        let mut permits = state.permits.lock().await;
        let permit = permits
            .get_mut(&uuid)
            .ok_or_else(|| anyhow::anyhow!("relay permit not found"))?;
        anyhow::ensure!(
            Instant::now() < permit.expires_at && permit.uses > 0,
            "relay permit expired"
        );
        permit.uses -= 1;
        if permit.uses == 0 {
            permits.remove(&uuid);
        }
    }
    let peer = state.pending.lock().await.remove(&uuid);
    if let Some(peer) = peer {
        let _active = state.metrics.start();
        relay_connections(connection, peer.connection, state.metrics.clone()).await?;
    } else {
        state.pending.lock().await.insert(
            uuid,
            Pending {
                connection,
                created_at: Instant::now(),
            },
        );
    }
    Ok(())
}

async fn relay_connections(
    first: RelayConnection,
    second: RelayConnection,
    metrics: Arc<RelayMetrics>,
) -> anyhow::Result<()> {
    match (first, second) {
        (RelayConnection::Tcp(first), RelayConnection::Tcp(second)) => {
            let mut first = metrics.meter(first);
            let mut second = metrics.meter(second);
            tokio::io::copy_bidirectional(&mut first, &mut second).await?;
        }
        (RelayConnection::WebSocket(first), RelayConnection::WebSocket(second)) => {
            relay_websockets(*first, *second, metrics).await?
        }
        (RelayConnection::WebSocket(websocket), RelayConnection::Tcp(tcp))
        | (RelayConnection::Tcp(tcp), RelayConnection::WebSocket(websocket)) => {
            relay_mixed(*websocket, tcp, metrics).await?
        }
    }
    Ok(())
}

async fn relay_websockets(
    first: WebSocketStream<TcpStream>,
    second: WebSocketStream<TcpStream>,
    metrics: Arc<RelayMetrics>,
) -> anyhow::Result<()> {
    let (mut first_tx, mut first_rx) = first.split();
    let (mut second_tx, mut second_rx) = second.split();
    loop {
        tokio::select! {
            value=first_rx.next()=>{let Some(value)=value else {break};let value=value?;if let WebSocketMessage::Binary(data)=&value{metrics.record_bytes(data.len())}second_tx.send(value).await?;},
            value=second_rx.next()=>{let Some(value)=value else {break};let value=value?;if let WebSocketMessage::Binary(data)=&value{metrics.record_bytes(data.len())}first_tx.send(value).await?;},
        }
    }
    Ok(())
}

async fn relay_mixed(
    websocket: WebSocketStream<TcpStream>,
    mut tcp: TcpStream,
    metrics: Arc<RelayMetrics>,
) -> anyhow::Result<()> {
    let (mut ws_tx, mut ws_rx) = websocket.split();
    let mut buffer = vec![0u8; 64 * 1024];
    loop {
        tokio::select! {
            value=ws_rx.next()=>{let Some(value)=value else {break};match value?{WebSocketMessage::Binary(data)=>{metrics.record_bytes(data.len());tcp.write_all(&data).await?},WebSocketMessage::Close(_)=>break,WebSocketMessage::Ping(data)=>ws_tx.send(WebSocketMessage::Pong(data)).await?,_=>{}}},
            read=tcp.read(&mut buffer)=>{let count=read?;if count==0{break}metrics.record_bytes(count);ws_tx.send(WebSocketMessage::Binary(Bytes::copy_from_slice(&buffer[..count]))).await?;},
        }
    }
    Ok(())
}

async fn read_frame(stream: &mut TcpStream) -> anyhow::Result<Vec<u8>> {
    let first = stream.read_u8().await?;
    let header_length = usize::from((first & 3) + 1);
    let mut header = [0u8; 4];
    header[0] = first;
    if header_length > 1 {
        stream.read_exact(&mut header[1..header_length]).await?;
    }
    let mut packed = 0usize;
    for (index, byte) in header[..header_length].iter().enumerate() {
        packed |= usize::from(*byte) << (8 * index);
    }
    let length = packed >> 2;
    anyhow::ensure!(length <= 1024 * 1024, "relay handshake too large");
    let mut frame = vec![0u8; length];
    stream.read_exact(&mut frame).await?;
    Ok(frame)
}

async fn cleanup(state: Arc<RelayState>) {
    let mut interval = tokio::time::interval(Duration::from_secs(30));
    loop {
        interval.tick().await;
        let now = Instant::now();
        state
            .permits
            .lock()
            .await
            .retain(|_, permit| permit.expires_at > now);
        state
            .pending
            .lock()
            .await
            .retain(|_, pending| pending.created_at.elapsed() < Duration::from_secs(90));
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use futures_util::{SinkExt, StreamExt};
    use tokio::net::{TcpListener, TcpStream};
    use tokio_tungstenite::{WebSocketStream, accept_async, client_async, tungstenite::Message};

    use super::{RelayMetrics, relay_websockets};

    async fn websocket_pair() -> (WebSocketStream<TcpStream>, WebSocketStream<TcpStream>) {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            accept_async(stream).await.unwrap()
        });
        let stream = TcpStream::connect(address).await.unwrap();
        let (client, _) = client_async(format!("ws://{address}/ws/relay"), stream)
            .await
            .unwrap();
        (client, server.await.unwrap())
    }

    #[tokio::test]
    async fn websocket_relay_forwards_binary_frames_and_counts_bytes() {
        let (mut first_client, first_server) = websocket_pair().await;
        let (mut second_client, second_server) = websocket_pair().await;
        let metrics = Arc::new(RelayMetrics::default());
        let relay_metrics = metrics.clone();
        let relay = tokio::spawn(async move {
            relay_websockets(first_server, second_server, relay_metrics)
                .await
                .unwrap();
        });
        first_client
            .send(Message::Binary(bytes::Bytes::from_static(
                b"encrypted-relay-data",
            )))
            .await
            .unwrap();
        let received = second_client.next().await.unwrap().unwrap().into_data();
        assert_eq!(received.as_ref(), b"encrypted-relay-data");
        assert_eq!(metrics.total_bytes(), 20);
        first_client.close(None).await.unwrap();
        second_client.close(None).await.unwrap();
        relay.await.unwrap();
    }
}
