use std::{net::SocketAddr, sync::Arc};

use art_core::protocol::{
    NatType, PunchFailure, PunchHole, PunchHoleResponse, RegisterPeerResponse, RegisterPkResponse,
    RegisterPkResult, RelayResponse, RendezvousMessage, TestNatResponse, encode_socket_addr,
    punch_hole_response, relay_response, rendezvous_message,
};
use art_core::transport::{SecureFramed, ServerKey};
use base64::{Engine as _, engine::general_purpose::STANDARD};
use bytes::Bytes;
use futures_util::{SinkExt, StreamExt};
use prost::Message;
use serde::Serialize;
use serde_json::json;
use tokio::net::{TcpListener, TcpStream, UdpSocket};
use tokio_tungstenite::{WebSocketStream, accept_async, tungstenite::Message as WebSocketMessage};

use crate::{gate::ConnectionGate, registry::PeerRegistry};

pub struct RendezvousServer {
    registry: Arc<PeerRegistry>,
    gate: Arc<ConnectionGate>,
    udp: Arc<UdpSocket>,
    relay_server: String,
    relay_control_address: String,
    internal_token: String,
    api_base: String,
    http: reqwest::Client,
    report_slots: Arc<tokio::sync::Semaphore>,
    server_key: Arc<ServerKey>,
}

pub struct RendezvousOptions {
    pub relay_server: String,
    pub relay_control_address: String,
    pub internal_token: String,
    pub api_base: String,
    pub server_key: Arc<ServerKey>,
}

enum ResponseConnection<'a> {
    Secure(&'a mut SecureFramed),
    WebSocket(&'a mut WebSocketStream<TcpStream>),
}

impl ResponseConnection<'_> {
    async fn send(&mut self, message: RendezvousMessage) -> anyhow::Result<()> {
        match self {
            Self::Secure(connection) => connection.send_message(message).await,
            Self::WebSocket(connection) => {
                connection
                    .send(WebSocketMessage::Binary(Bytes::from(
                        message.encode_to_vec(),
                    )))
                    .await?;
                Ok(())
            }
        }
    }
}

impl RendezvousServer {
    pub fn online_peers(&self) -> usize {
        self.registry.active_count()
    }

    pub async fn bind(
        tcp_address: &str,
        udp_address: &str,
        gate: Arc<ConnectionGate>,
        options: RendezvousOptions,
    ) -> anyhow::Result<(Arc<Self>, TcpListener)> {
        let tcp = TcpListener::bind(tcp_address).await?;
        let udp = Arc::new(UdpSocket::bind(udp_address).await?);
        Ok((
            Arc::new(Self {
                registry: Arc::new(PeerRegistry::default()),
                gate,
                udp,
                relay_server: options.relay_server,
                relay_control_address: options.relay_control_address,
                internal_token: options.internal_token,
                api_base: options.api_base,
                http: reqwest::Client::new(),
                report_slots: Arc::new(tokio::sync::Semaphore::new(64)),
                server_key: options.server_key,
            }),
            tcp,
        ))
    }

    pub async fn run(
        self: Arc<Self>,
        listener: TcpListener,
        nat_listener: TcpListener,
        websocket_listener: TcpListener,
    ) -> anyhow::Result<()> {
        let udp_server = self.clone();
        tokio::spawn(async move {
            if let Err(error) = udp_server.run_udp().await {
                tracing::error!(%error, "UDP rendezvous stopped");
            }
        });
        let websocket_server = self.clone();
        tokio::spawn(async move {
            loop {
                let Ok((stream, address)) = websocket_listener.accept().await else {
                    break;
                };
                let server = websocket_server.clone();
                tokio::spawn(async move {
                    if let Err(error) = server.handle_websocket(stream, address).await {
                        tracing::debug!(%address, %error, "WebSocket rendezvous connection closed");
                    }
                });
            }
        });
        tokio::spawn(async move {
            loop {
                let Ok((stream, address)) = nat_listener.accept().await else {
                    break;
                };
                tokio::spawn(async move {
                    if let Err(error) = handle_nat(stream, address).await {
                        tracing::debug!(%address, %error, "NAT probe closed");
                    }
                });
            }
        });
        loop {
            let (stream, address) = listener.accept().await?;
            let server = self.clone();
            tokio::spawn(async move {
                if let Err(error) = server.handle_tcp(stream, address).await {
                    tracing::debug!(%address, %error, "TCP rendezvous connection closed");
                }
            });
        }
    }

    async fn run_udp(&self) -> anyhow::Result<()> {
        let mut buffer = vec![0u8; 64 * 1024];
        loop {
            let (length, address) = self.udp.recv_from(&mut buffer).await?;
            let Ok(message) = RendezvousMessage::decode(&buffer[..length]) else {
                continue;
            };
            match message.union {
                Some(rendezvous_message::Union::RegisterPeer(register)) => {
                    self.registry.register(register.id.clone(), address, None);
                    self.report_device(register.id, String::new(), address.ip());
                    self.send_udp(
                        address,
                        RendezvousMessage {
                            union: Some(rendezvous_message::Union::RegisterPeerResponse(
                                RegisterPeerResponse { request_pk: true },
                            )),
                        },
                    )
                    .await?;
                }
                Some(rendezvous_message::Union::RegisterPk(register)) => {
                    let public_key = STANDARD.encode(&register.pk);
                    if !self
                        .gate
                        .device_registration_allowed(&register.id, &public_key)
                    {
                        self.send_udp(
                            address,
                            RendezvousMessage {
                                union: Some(rendezvous_message::Union::RegisterPkResponse(
                                    RegisterPkResponse {
                                        result: RegisterPkResult::NotDeployed as i32,
                                        keep_alive: 120_000,
                                    },
                                )),
                            },
                        )
                        .await?;
                        continue;
                    }
                    let client_uuid = encode_hex(&register.uuid);
                    self.registry
                        .register(register.id.clone(), address, Some(register.pk));
                    self.report_device(register.id, client_uuid, address.ip());
                    self.send_udp(
                        address,
                        RendezvousMessage {
                            union: Some(rendezvous_message::Union::RegisterPkResponse(
                                RegisterPkResponse {
                                    result: RegisterPkResult::Ok as i32,
                                    keep_alive: 120_000,
                                },
                            )),
                        },
                    )
                    .await?;
                }
                Some(rendezvous_message::Union::TestNatRequest(_)) => {
                    self.send_udp(
                        address,
                        RendezvousMessage {
                            union: Some(rendezvous_message::Union::TestNatResponse(
                                TestNatResponse {
                                    port: i32::from(address.port()),
                                },
                            )),
                        },
                    )
                    .await?;
                }
                _ => {}
            }
        }
    }

    async fn handle_tcp(&self, stream: TcpStream, address: SocketAddr) -> anyhow::Result<()> {
        let framed = tokio_util::codec::Framed::new(
            stream,
            art_core::protocol::BytesCodec::new(1024 * 1024),
        );
        let mut connection = SecureFramed::accept(framed, &self.server_key).await?;
        while let Some(message) = connection.next_message().await? {
            match message.union {
                Some(rendezvous_message::Union::PunchHoleRequest(mut request)) => {
                    match self
                        .gate
                        .authorize_punch(&request, &address.ip().to_string())
                    {
                        Err(reason) => {
                            connection
                                .send_message(RendezvousMessage {
                                    union: Some(rendezvous_message::Union::PunchHoleResponse(
                                        denied_punch(reason),
                                    )),
                                })
                                .await?
                        }
                        Ok(decision) => {
                            request.force_relay |= decision.force_relay;
                            self.process_punch(
                                &mut ResponseConnection::Secure(&mut connection),
                                address,
                                request,
                            )
                            .await?
                        }
                    }
                }
                Some(rendezvous_message::Union::RequestRelay(request)) => {
                    match self
                        .gate
                        .authorize_relay(&request, &address.ip().to_string())
                    {
                        Err(reason) => {
                            connection
                                .send_message(RendezvousMessage {
                                    union: Some(rendezvous_message::Union::RelayResponse(
                                        RelayResponse {
                                            refuse_reason: reason,
                                            ..Default::default()
                                        },
                                    )),
                                })
                                .await?
                        }
                        Ok(_) => {
                            self.process_relay(
                                &mut ResponseConnection::Secure(&mut connection),
                                address,
                                request,
                            )
                            .await?
                        }
                    }
                }
                Some(rendezvous_message::Union::RegisterPeer(register)) => {
                    self.registry.register(register.id.clone(), address, None);
                    self.report_device(register.id, String::new(), address.ip());
                    connection
                        .send_message(RendezvousMessage {
                            union: Some(rendezvous_message::Union::RegisterPeerResponse(
                                RegisterPeerResponse { request_pk: true },
                            )),
                        })
                        .await?;
                }
                Some(rendezvous_message::Union::RegisterPk(register)) => {
                    let public_key = STANDARD.encode(&register.pk);
                    if !self
                        .gate
                        .device_registration_allowed(&register.id, &public_key)
                    {
                        connection
                            .send_message(RendezvousMessage {
                                union: Some(rendezvous_message::Union::RegisterPkResponse(
                                    RegisterPkResponse {
                                        result: RegisterPkResult::NotDeployed as i32,
                                        keep_alive: 120_000,
                                    },
                                )),
                            })
                            .await?;
                        continue;
                    }
                    let client_uuid = encode_hex(&register.uuid);
                    self.registry
                        .register(register.id.clone(), address, Some(register.pk));
                    self.report_device(register.id, client_uuid, address.ip());
                    connection
                        .send_message(RendezvousMessage {
                            union: Some(rendezvous_message::Union::RegisterPkResponse(
                                RegisterPkResponse {
                                    result: RegisterPkResult::Ok as i32,
                                    keep_alive: 120_000,
                                },
                            )),
                        })
                        .await?;
                }
                Some(rendezvous_message::Union::TestNatRequest(_)) => {
                    connection
                        .send_message(RendezvousMessage {
                            union: Some(rendezvous_message::Union::TestNatResponse(
                                TestNatResponse {
                                    port: i32::from(address.port()),
                                },
                            )),
                        })
                        .await?;
                }
                _ => {}
            }
        }
        Ok(())
    }

    async fn handle_websocket(&self, stream: TcpStream, address: SocketAddr) -> anyhow::Result<()> {
        let mut connection = accept_async(stream).await?;
        while let Some(frame) = connection.next().await {
            match frame? {
                WebSocketMessage::Binary(bytes) => {
                    let message = RendezvousMessage::decode(bytes)?;
                    match message.union {
                        Some(rendezvous_message::Union::PunchHoleRequest(mut request)) => {
                            match self
                                .gate
                                .authorize_punch(&request, &address.ip().to_string())
                            {
                                Err(reason) => {
                                    ResponseConnection::WebSocket(&mut connection)
                                        .send(RendezvousMessage {
                                            union: Some(
                                                rendezvous_message::Union::PunchHoleResponse(
                                                    denied_punch(reason),
                                                ),
                                            ),
                                        })
                                        .await?
                                }
                                Ok(decision) => {
                                    request.force_relay |= decision.force_relay;
                                    self.process_punch(
                                        &mut ResponseConnection::WebSocket(&mut connection),
                                        address,
                                        request,
                                    )
                                    .await?;
                                }
                            }
                        }
                        Some(rendezvous_message::Union::RequestRelay(request)) => {
                            match self
                                .gate
                                .authorize_relay(&request, &address.ip().to_string())
                            {
                                Err(reason) => {
                                    ResponseConnection::WebSocket(&mut connection)
                                        .send(RendezvousMessage {
                                            union: Some(rendezvous_message::Union::RelayResponse(
                                                RelayResponse {
                                                    refuse_reason: reason,
                                                    ..Default::default()
                                                },
                                            )),
                                        })
                                        .await?
                                }
                                Ok(_) => {
                                    self.process_relay(
                                        &mut ResponseConnection::WebSocket(&mut connection),
                                        address,
                                        request,
                                    )
                                    .await?
                                }
                            }
                        }
                        Some(rendezvous_message::Union::TestNatRequest(_)) => {
                            ResponseConnection::WebSocket(&mut connection)
                                .send(RendezvousMessage {
                                    union: Some(rendezvous_message::Union::TestNatResponse(
                                        TestNatResponse {
                                            port: i32::from(address.port()),
                                        },
                                    )),
                                })
                                .await?;
                        }
                        _ => {}
                    }
                }
                WebSocketMessage::Ping(payload) => {
                    connection.send(WebSocketMessage::Pong(payload)).await?
                }
                WebSocketMessage::Close(_) => break,
                _ => {}
            }
        }
        Ok(())
    }

    async fn process_punch(
        &self,
        connection: &mut ResponseConnection<'_>,
        controller: SocketAddr,
        request: art_core::protocol::PunchHoleRequest,
    ) -> anyhow::Result<()> {
        let Some(target) = self.registry.get(&request.id) else {
            return connection
                .send(RendezvousMessage {
                    union: Some(rendezvous_message::Union::PunchHoleResponse(
                        PunchHoleResponse {
                            failure: PunchFailure::IdNotExist as i32,
                            ..Default::default()
                        },
                    )),
                })
                .await;
        };
        let relay_server = self.gate.select_relay(&self.relay_server);
        let signed_peer_key = self
            .server_key
            .sign_peer_key(&request.id, &target.public_key)?;
        let instruction = PunchHole {
            socket_addr: encode_socket_addr(controller),
            relay_server: relay_server.clone(),
            nat_type: request.nat_type,
            udp_port: request.udp_port,
            force_relay: request.force_relay,
            upnp_port: request.upnp_port,
            socket_addr_v6: request.socket_addr_v6.clone(),
        };
        self.send_udp(
            target.address,
            RendezvousMessage {
                union: Some(rendezvous_message::Union::PunchHole(instruction)),
            },
        )
        .await?;
        connection
            .send(RendezvousMessage {
                union: Some(rendezvous_message::Union::PunchHoleResponse(
                    PunchHoleResponse {
                        socket_addr: encode_socket_addr(target.address),
                        pk: signed_peer_key,
                        relay_server,
                        union: Some(punch_hole_response::Union::NatType(
                            NatType::UnknownNat as i32,
                        )),
                        ..Default::default()
                    },
                )),
            })
            .await
    }

    async fn process_relay(
        &self,
        connection: &mut ResponseConnection<'_>,
        controller: SocketAddr,
        mut request: art_core::protocol::RequestRelay,
    ) -> anyhow::Result<()> {
        let Some(target) = self.registry.get(&request.id) else {
            return connection
                .send(RendezvousMessage {
                    union: Some(rendezvous_message::Union::RelayResponse(RelayResponse {
                        refuse_reason: "Connection denied: target offline".into(),
                        ..Default::default()
                    })),
                })
                .await;
        };
        if request.uuid.is_empty() {
            request.uuid = uuid::Uuid::new_v4().to_string();
        }
        let relay_server = self.gate.select_relay(&self.relay_server);
        let signed_peer_key = self
            .server_key
            .sign_peer_key(&request.id, &target.public_key)?;
        self.issue_relay_permit(&request.uuid, &relay_server)
            .await?;
        request.socket_addr = encode_socket_addr(controller);
        request.relay_server = relay_server.clone();
        request.token.clear();
        self.send_udp(
            target.address,
            RendezvousMessage {
                union: Some(rendezvous_message::Union::RequestRelay(request.clone())),
            },
        )
        .await?;
        connection
            .send(RendezvousMessage {
                union: Some(rendezvous_message::Union::RelayResponse(RelayResponse {
                    socket_addr: encode_socket_addr(target.address),
                    uuid: request.uuid,
                    relay_server,
                    union: Some(relay_response::Union::Pk(signed_peer_key)),
                    ..Default::default()
                })),
            })
            .await
    }

    async fn issue_relay_permit(&self, uuid: &str, relay_server: &str) -> anyhow::Result<()> {
        let socket = UdpSocket::bind("0.0.0.0:0").await?;
        let payload =
            json!({"token": self.internal_token, "uuid": uuid, "expires_in": 60}).to_string();
        let control_address = relay_control_address(relay_server)
            .unwrap_or_else(|| self.relay_control_address.clone());
        socket.send_to(payload.as_bytes(), &control_address).await?;
        Ok(())
    }

    fn report_device(&self, rustdesk_id: String, client_uuid: String, address: std::net::IpAddr) {
        let Ok(permit) = self.report_slots.clone().try_acquire_owned() else {
            tracing::debug!(%rustdesk_id, "device heartbeat queue is saturated");
            return;
        };
        let http = self.http.clone();
        let url = format!("{}/internal/v1/devices/heartbeat", self.api_base);
        let token = self.internal_token.clone();
        tokio::spawn(async move {
            let _permit = permit;
            let result = http
                .post(url)
                .header("X-RDS-Internal-Token", token)
                .json(&DeviceHeartbeat {
                    rustdesk_id,
                    client_uuid,
                    last_seen_ip: address.to_string(),
                })
                .send()
                .await;
            if let Err(error) = result {
                tracing::debug!(%error, "device heartbeat delivery failed");
            }
        });
    }

    async fn send_udp(
        &self,
        address: SocketAddr,
        message: RendezvousMessage,
    ) -> anyhow::Result<()> {
        let mut bytes = Vec::with_capacity(message.encoded_len());
        message.encode(&mut bytes)?;
        self.udp.send_to(&bytes, address).await?;
        Ok(())
    }
}

#[derive(Serialize)]
struct DeviceHeartbeat {
    rustdesk_id: String,
    client_uuid: String,
    last_seen_ip: String,
}

fn encode_hex(value: &[u8]) -> String {
    value.iter().map(|byte| format!("{byte:02x}")).collect()
}

async fn handle_nat(stream: TcpStream, address: SocketAddr) -> anyhow::Result<()> {
    let mut framed =
        tokio_util::codec::Framed::new(stream, art_core::protocol::BytesCodec::new(64 * 1024));
    let Some(frame) = framed.next().await else {
        return Ok(());
    };
    let message = RendezvousMessage::decode(frame?.freeze())?;
    if matches!(
        message.union,
        Some(rendezvous_message::Union::TestNatRequest(_))
    ) {
        let response = RendezvousMessage {
            union: Some(rendezvous_message::Union::TestNatResponse(
                TestNatResponse {
                    port: i32::from(address.port()),
                },
            )),
        };
        let mut data = Vec::with_capacity(response.encoded_len());
        response.encode(&mut data)?;
        framed.send(Bytes::from(data)).await?;
    }
    Ok(())
}

fn denied_punch(reason: String) -> PunchHoleResponse {
    PunchHoleResponse {
        other_failure: reason,
        ..Default::default()
    }
}

fn relay_control_address(relay_server: &str) -> Option<String> {
    let (host, port) = relay_server.rsplit_once(':')?;
    port.parse::<u16>().ok()?;
    Some(format!("{host}:21119"))
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use art_core::{
        audit::AuditSender,
        auth::{AuthCache, JwtVerifier},
        protocol::{PunchHoleRequest, rendezvous_message},
    };
    use futures_util::{SinkExt, StreamExt};
    use prost::Message;
    use tokio::net::{TcpListener, TcpStream};
    use tokio_tungstenite::{client_async, tungstenite::Message as WebSocketMessage};

    use super::{RendezvousOptions, RendezvousServer, relay_control_address};
    use crate::gate::ConnectionGate;

    #[test]
    fn derives_control_endpoint_for_hostname_and_ipv6() {
        assert_eq!(
            relay_control_address("relay.example:21117").as_deref(),
            Some("relay.example:21119")
        );
        assert_eq!(
            relay_control_address("[2001:db8::1]:21117").as_deref(),
            Some("[2001:db8::1]:21119")
        );
    }

    #[tokio::test]
    async fn websocket_punch_is_authenticated_before_target_lookup() {
        let cache = Arc::new(AuthCache::new(
            JwtVerifier::hs256(
                b"0123456789abcdef0123456789abcdef",
                "art-rustdesk",
                "art-hbbs",
            )
            .unwrap(),
            true,
        ));
        let gate = Arc::new(ConnectionGate::new(cache, AuditSender::discard(), false));
        let key_path = std::env::temp_dir().join(format!("art-hbbs-ws-{}", uuid::Uuid::new_v4()));
        let server_key =
            Arc::new(art_core::transport::ServerKey::load_or_create(&key_path).unwrap());
        let (server, _tcp) = RendezvousServer::bind(
            "127.0.0.1:0",
            "127.0.0.1:0",
            gate,
            RendezvousOptions {
                relay_server: "127.0.0.1:21117".into(),
                relay_control_address: "127.0.0.1:21119".into(),
                internal_token: "0123456789abcdef0123456789abcdef".into(),
                api_base: "http://127.0.0.1:1".into(),
                server_key,
            },
        )
        .await
        .unwrap();
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let task = tokio::spawn(async move {
            let (stream, peer) = listener.accept().await.unwrap();
            server.handle_websocket(stream, peer).await.unwrap();
        });
        let stream = TcpStream::connect(address).await.unwrap();
        let (mut websocket, _) = client_async(format!("ws://{address}/ws/id"), stream)
            .await
            .unwrap();
        let request = art_core::protocol::RendezvousMessage {
            union: Some(rendezvous_message::Union::PunchHoleRequest(
                PunchHoleRequest {
                    id: "missing-target".into(),
                    ..Default::default()
                },
            )),
        };
        websocket
            .send(WebSocketMessage::Binary(request.encode_to_vec().into()))
            .await
            .unwrap();
        let response = websocket.next().await.unwrap().unwrap().into_data();
        let response = art_core::protocol::RendezvousMessage::decode(response).unwrap();
        let Some(rendezvous_message::Union::PunchHoleResponse(response)) = response.union else {
            panic!("unexpected response")
        };
        assert_eq!(
            response.other_failure,
            "Для подключения необходимо войти в аккаунт RustDesk"
        );
        websocket.close(None).await.unwrap();
        task.await.unwrap();
        let _ = std::fs::remove_file(&key_path);
    }
}
