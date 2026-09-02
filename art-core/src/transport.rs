use std::{
    fs, io,
    path::{Path, PathBuf},
};

use base64::{
    Engine as _, alphabet,
    engine::{
        DecodePaddingMode, GeneralPurpose, GeneralPurposeConfig, general_purpose::STANDARD_NO_PAD,
    },
};
use bytes::Bytes;
use dryoc::{
    classic::{
        crypto_box::{
            Nonce as BoxNonce, PublicKey as BoxPublicKey, crypto_box_keypair, crypto_box_open_easy,
        },
        crypto_secretbox::{
            Key as SecretKey, Nonce as SecretNonce, crypto_secretbox_easy,
            crypto_secretbox_open_easy,
        },
        crypto_sign::{
            PublicKey as SigningPublicKey, SecretKey as SigningSecretKey, crypto_sign,
            crypto_sign_keypair,
        },
    },
    constants::{
        CRYPTO_BOX_MACBYTES, CRYPTO_SECRETBOX_KEYBYTES, CRYPTO_SECRETBOX_MACBYTES,
        CRYPTO_SIGN_BYTES,
    },
};
use futures_util::{SinkExt, StreamExt};
use prost::Message;
use tokio::{
    net::TcpStream,
    time::{Duration, timeout},
};
use tokio_util::codec::Framed;

use crate::protocol::{BytesCodec, IdPk, KeyExchange, RendezvousMessage, rendezvous_message};

pub struct ServerKey {
    public: SigningPublicKey,
    secret: SigningSecretKey,
}

impl ServerKey {
    pub fn load_or_create(path: &Path) -> anyhow::Result<Self> {
        let key = match fs::read_to_string(path) {
            Ok(value) => {
                match secure_secret_permissions(path) {
                    Ok(()) => {}
                    Err(error) if error.kind() == io::ErrorKind::PermissionDenied => {}
                    Err(error) => return Err(error.into()),
                }
                let secret_bytes = decode_standard_base64(value.trim())?;
                let secret: SigningSecretKey = secret_bytes
                    .try_into()
                    .map_err(|_| anyhow::anyhow!("server signing key has invalid length"))?;
                let mut public = [0u8; 32];
                public.copy_from_slice(&secret[32..]);
                Self { public, secret }
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                let (public, secret) = crypto_sign_keypair();
                persist_secret(path, STANDARD_NO_PAD.encode(secret).as_bytes())?;
                Self { public, secret }
            }
            Err(error) => return Err(error.into()),
        };
        let public_path = path.with_extension("pub");
        if !public_key_matches(&public_path, &key.public) {
            fs::write(public_path, format!("{}\n", key.public_key_base64()))?;
        }
        Ok(key)
    }

    pub fn public_key_base64(&self) -> String {
        STANDARD_NO_PAD.encode(self.public)
    }

    /// RustDesk clients expect `PunchHoleResponse.pk` to contain a protobuf
    /// `IdPk` signed by the rendezvous server, not the raw peer public key.
    pub fn sign_peer_key(&self, id: &str, peer_public_key: &[u8]) -> anyhow::Result<Vec<u8>> {
        if peer_public_key.len() != 32 {
            return Ok(Vec::new());
        }
        let payload = IdPk {
            id: id.to_owned(),
            pk: peer_public_key.to_vec(),
        }
        .encode_to_vec();
        let mut signed = vec![0u8; payload.len() + CRYPTO_SIGN_BYTES];
        crypto_sign(&mut signed, &payload, &self.secret)?;
        Ok(signed)
    }
}

fn decode_standard_base64(value: &str) -> anyhow::Result<Vec<u8>> {
    let engine = GeneralPurpose::new(
        &alphabet::STANDARD,
        GeneralPurposeConfig::new().with_decode_padding_mode(DecodePaddingMode::Indifferent),
    );
    Ok(engine.decode(value)?)
}

fn public_key_matches(path: &Path, expected: &SigningPublicKey) -> bool {
    fs::read_to_string(path)
        .ok()
        .and_then(|value| decode_standard_base64(value.trim()).ok())
        .is_some_and(|value| value.as_slice() == expected)
}

fn secure_secret_permissions(path: &Path) -> io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    }
    #[cfg(not(unix))]
    let _ = path;
    Ok(())
}

pub struct SecureFramed {
    inner: Framed<TcpStream, BytesCodec>,
    key: Option<SecretKey>,
    pending_plaintext: Option<RendezvousMessage>,
    send_sequence: u64,
    receive_sequence: u64,
}

impl SecureFramed {
    pub async fn accept(
        mut inner: Framed<TcpStream, BytesCodec>,
        signing_key: &ServerKey,
    ) -> anyhow::Result<Self> {
        let (box_public, box_secret) = crypto_box_keypair();
        let mut signed_public = vec![0u8; box_public.len() + CRYPTO_SIGN_BYTES];
        crypto_sign(&mut signed_public, &box_public, &signing_key.secret)?;
        send_plain(
            &mut inner,
            RendezvousMessage {
                union: Some(rendezvous_message::Union::KeyExchange(KeyExchange {
                    keys: vec![signed_public],
                })),
            },
        )
        .await?;
        let frame = timeout(Duration::from_secs(10), inner.next())
            .await
            .map_err(|_| anyhow::anyhow!("secure handshake timed out"))?
            .ok_or_else(|| anyhow::anyhow!("peer closed during secure handshake"))??;
        let message = RendezvousMessage::decode(frame.freeze())?;
        let exchange = match message.union.as_ref() {
            Some(rendezvous_message::Union::KeyExchange(value)) if value.keys.len() == 2 => value,
            _ => {
                return Ok(Self {
                    inner,
                    key: None,
                    pending_plaintext: Some(message),
                    send_sequence: 0,
                    receive_sequence: 0,
                });
            }
        };
        let peer_public: BoxPublicKey = exchange.keys[0]
            .as_slice()
            .try_into()
            .map_err(|_| anyhow::anyhow!("client box public key has invalid length"))?;
        anyhow::ensure!(
            exchange.keys[1].len() >= CRYPTO_BOX_MACBYTES,
            "encrypted session key is truncated"
        );
        let mut key = [0u8; CRYPTO_SECRETBOX_KEYBYTES];
        crypto_box_open_easy(
            &mut key,
            &exchange.keys[1],
            &BoxNonce::default(),
            &peer_public,
            &box_secret,
        )?;
        Ok(Self {
            inner,
            key: Some(key),
            pending_plaintext: None,
            send_sequence: 0,
            receive_sequence: 0,
        })
    }

    pub async fn next_message(&mut self) -> anyhow::Result<Option<RendezvousMessage>> {
        if let Some(message) = self.pending_plaintext.take() {
            return Ok(Some(message));
        }
        let Some(frame) = self.inner.next().await else {
            return Ok(None);
        };
        let frame = frame?;
        let Some(key) = self.key.as_ref() else {
            return Ok(Some(RendezvousMessage::decode(frame.freeze())?));
        };
        let ciphertext = frame;
        anyhow::ensure!(
            ciphertext.len() >= CRYPTO_SECRETBOX_MACBYTES,
            "encrypted frame is truncated"
        );
        self.receive_sequence = self
            .receive_sequence
            .checked_add(1)
            .ok_or_else(|| anyhow::anyhow!("receive nonce exhausted"))?;
        let mut plaintext = vec![0u8; ciphertext.len() - CRYPTO_SECRETBOX_MACBYTES];
        crypto_secretbox_open_easy(
            &mut plaintext,
            &ciphertext,
            &sequence_nonce(self.receive_sequence),
            key,
        )?;
        Ok(Some(RendezvousMessage::decode(plaintext.as_slice())?))
    }

    pub async fn send_message(&mut self, message: RendezvousMessage) -> anyhow::Result<()> {
        let Some(key) = self.key.as_ref() else {
            return send_plain(&mut self.inner, message).await;
        };
        let mut plaintext = Vec::with_capacity(message.encoded_len());
        message.encode(&mut plaintext)?;
        self.send_sequence = self
            .send_sequence
            .checked_add(1)
            .ok_or_else(|| anyhow::anyhow!("send nonce exhausted"))?;
        let mut ciphertext = vec![0u8; plaintext.len() + CRYPTO_SECRETBOX_MACBYTES];
        crypto_secretbox_easy(
            &mut ciphertext,
            &plaintext,
            &sequence_nonce(self.send_sequence),
            key,
        )?;
        self.inner.send(Bytes::from(ciphertext)).await?;
        Ok(())
    }
}

fn sequence_nonce(sequence: u64) -> SecretNonce {
    let mut nonce = SecretNonce::default();
    nonce[..8].copy_from_slice(&sequence.to_le_bytes());
    nonce
}

async fn send_plain(
    framed: &mut Framed<TcpStream, BytesCodec>,
    message: RendezvousMessage,
) -> anyhow::Result<()> {
    let mut data = Vec::with_capacity(message.encoded_len());
    message.encode(&mut data)?;
    framed.send(Bytes::from(data)).await?;
    Ok(())
}

fn persist_secret(path: &Path, secret: &[u8]) -> anyhow::Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let temporary = PathBuf::from(format!("{}.tmp", path.display()));
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&temporary)?;
    use std::io::Write;
    file.write_all(secret)?;
    file.write_all(b"\n")?;
    file.sync_all()?;
    fs::rename(temporary, path)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::{
        sync::Arc,
        time::{SystemTime, UNIX_EPOCH},
    };

    use dryoc::classic::{
        crypto_box::{crypto_box_easy, crypto_box_keypair},
        crypto_secretbox::{crypto_secretbox_easy, crypto_secretbox_open_easy},
        crypto_sign::crypto_sign_open,
    };
    use tokio::net::TcpListener;

    use super::*;
    use crate::protocol::{PunchHoleRequest, PunchHoleResponse};

    #[test]
    fn loads_official_padded_rustdesk_key_pair() {
        let directory = std::env::temp_dir().join(format!(
            "art-core-key-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let private_path = directory.join("id_ed25519");
        let generated = ServerKey::load_or_create(&private_path).unwrap();
        let private_bytes =
            decode_standard_base64(std::fs::read_to_string(&private_path).unwrap().trim()).unwrap();
        let public_bytes = decode_standard_base64(&generated.public_key_base64()).unwrap();
        std::fs::write(
            &private_path,
            base64::engine::general_purpose::STANDARD.encode(private_bytes),
        )
        .unwrap();
        std::fs::write(
            private_path.with_extension("pub"),
            base64::engine::general_purpose::STANDARD.encode(public_bytes),
        )
        .unwrap();

        let imported = ServerKey::load_or_create(&private_path).unwrap();

        assert_eq!(imported.public_key_base64(), generated.public_key_base64());
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn signs_peer_identity_in_official_rustdesk_format() {
        let directory = std::env::temp_dir().join(format!(
            "art-core-id-pk-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let key = ServerKey::load_or_create(&directory.join("id_ed25519")).unwrap();
        let peer_key = [42u8; 32];
        let signed = key.sign_peer_key("226424246", &peer_key).unwrap();
        let mut payload = vec![0u8; signed.len() - CRYPTO_SIGN_BYTES];

        crypto_sign_open(&mut payload, &signed, &key.public).unwrap();
        let identity = IdPk::decode(payload.as_slice()).unwrap();

        assert_eq!(identity.id, "226424246");
        assert_eq!(identity.pk, peer_key);
        assert!(
            key.sign_peer_key("226424246", &[1, 2, 3])
                .unwrap()
                .is_empty()
        );
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    async fn rustdesk_secure_transport_round_trip() {
        let directory = std::env::temp_dir().join(format!(
            "art-core-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let key = Arc::new(ServerKey::load_or_create(&directory.join("id_ed25519")).unwrap());
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server_key = key.clone();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let framed = Framed::new(stream, BytesCodec::new(1024 * 1024));
            let mut secure = SecureFramed::accept(framed, &server_key).await.unwrap();
            let request = secure.next_message().await.unwrap().unwrap();
            assert!(matches!(
                request.union,
                Some(rendezvous_message::Union::PunchHoleRequest(_))
            ));
            secure
                .send_message(RendezvousMessage {
                    union: Some(rendezvous_message::Union::PunchHoleResponse(
                        PunchHoleResponse {
                            other_failure: "test-denial".into(),
                            ..Default::default()
                        },
                    )),
                })
                .await
                .unwrap();
        });

        let stream = TcpStream::connect(address).await.unwrap();
        let mut framed = Framed::new(stream, BytesCodec::new(1024 * 1024));
        let first = framed.next().await.unwrap().unwrap();
        let exchange = match RendezvousMessage::decode(first.freeze())
            .unwrap()
            .union
            .unwrap()
        {
            rendezvous_message::Union::KeyExchange(value) => value,
            _ => panic!("server did not start with key exchange"),
        };
        let mut server_box_public = [0u8; 32];
        crypto_sign_open(&mut server_box_public, &exchange.keys[0], &key.public).unwrap();
        let (client_public, client_secret) = crypto_box_keypair();
        let session_key = [9u8; CRYPTO_SECRETBOX_KEYBYTES];
        let mut encrypted_key = vec![0u8; session_key.len() + CRYPTO_BOX_MACBYTES];
        crypto_box_easy(
            &mut encrypted_key,
            &session_key,
            &BoxNonce::default(),
            &server_box_public,
            &client_secret,
        )
        .unwrap();
        send_plain(
            &mut framed,
            RendezvousMessage {
                union: Some(rendezvous_message::Union::KeyExchange(KeyExchange {
                    keys: vec![client_public.to_vec(), encrypted_key],
                })),
            },
        )
        .await
        .unwrap();

        let request = RendezvousMessage {
            union: Some(rendezvous_message::Union::PunchHoleRequest(
                PunchHoleRequest {
                    id: "123456789".into(),
                    token: "jwt".into(),
                    ..Default::default()
                },
            )),
        };
        let mut request_bytes = Vec::new();
        request.encode(&mut request_bytes).unwrap();
        let mut encrypted_request = vec![0u8; request_bytes.len() + CRYPTO_SECRETBOX_MACBYTES];
        crypto_secretbox_easy(
            &mut encrypted_request,
            &request_bytes,
            &sequence_nonce(1),
            &session_key,
        )
        .unwrap();
        framed.send(Bytes::from(encrypted_request)).await.unwrap();

        let encrypted_response = framed.next().await.unwrap().unwrap();
        let mut response_bytes = vec![0u8; encrypted_response.len() - CRYPTO_SECRETBOX_MACBYTES];
        crypto_secretbox_open_easy(
            &mut response_bytes,
            &encrypted_response,
            &sequence_nonce(1),
            &session_key,
        )
        .unwrap();
        let response = RendezvousMessage::decode(response_bytes.as_slice()).unwrap();
        assert!(
            matches!(response.union, Some(rendezvous_message::Union::PunchHoleResponse(value)) if value.other_failure == "test-denial")
        );
        server.await.unwrap();
        std::fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    async fn plaintext_client_receives_auth_denial() {
        let directory = std::env::temp_dir().join(format!(
            "art-core-plain-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let key = Arc::new(ServerKey::load_or_create(&directory.join("id_ed25519")).unwrap());
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            let framed = Framed::new(stream, BytesCodec::new(1024 * 1024));
            let mut connection = SecureFramed::accept(framed, &key).await.unwrap();
            let request = connection.next_message().await.unwrap().unwrap();
            assert!(matches!(
                request.union,
                Some(rendezvous_message::Union::PunchHoleRequest(value)) if value.token.is_empty()
            ));
            connection
                .send_message(RendezvousMessage {
                    union: Some(rendezvous_message::Union::PunchHoleResponse(
                        PunchHoleResponse {
                            other_failure: "Для подключения необходимо войти в аккаунт RustDesk"
                                .into(),
                            ..Default::default()
                        },
                    )),
                })
                .await
                .unwrap();
        });

        let stream = TcpStream::connect(address).await.unwrap();
        let mut framed = Framed::new(stream, BytesCodec::new(1024 * 1024));
        send_plain(
            &mut framed,
            RendezvousMessage {
                union: Some(rendezvous_message::Union::PunchHoleRequest(
                    PunchHoleRequest {
                        id: "123456789".into(),
                        ..Default::default()
                    },
                )),
            },
        )
        .await
        .unwrap();

        let hello = framed.next().await.unwrap().unwrap();
        assert!(matches!(
            RendezvousMessage::decode(hello.freeze()).unwrap().union,
            Some(rendezvous_message::Union::KeyExchange(_))
        ));
        let denial = framed.next().await.unwrap().unwrap();
        let denial = RendezvousMessage::decode(denial.freeze()).unwrap();
        assert!(
            matches!(denial.union, Some(rendezvous_message::Union::PunchHoleResponse(value)) if value.other_failure == "Для подключения необходимо войти в аккаунт RustDesk")
        );

        server.await.unwrap();
        std::fs::remove_dir_all(directory).unwrap();
    }
}
