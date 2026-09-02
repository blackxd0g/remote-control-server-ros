use std::time::{SystemTime, UNIX_EPOCH};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use sha2::Sha256;

use super::AuthError;

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(untagged)]
pub enum Audience {
    One(String),
    Many(Vec<String>),
}

impl Audience {
    fn contains(&self, expected: &str) -> bool {
        match self {
            Self::One(value) => value == expected,
            Self::Many(values) => values.iter().any(|value| value == expected),
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Claims {
    pub sub: String,
    pub sid: String,
    pub iat: u64,
    pub exp: u64,
    pub iss: String,
    pub aud: Audience,
    #[serde(default)]
    pub username: String,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub token_version: i64,
    #[serde(default)]
    pub device_id: String,
}

#[derive(Deserialize)]
struct Header {
    alg: String,
}

#[derive(Clone)]
pub struct JwtVerifier {
    secret: Vec<u8>,
    issuer: String,
    audience: String,
}

impl JwtVerifier {
    pub fn hs256(secret: &[u8], issuer: &str, audience: &str) -> anyhow::Result<Self> {
        anyhow::ensure!(secret.len() >= 32, "JWT secret must be at least 32 bytes");
        Ok(Self {
            secret: secret.to_vec(),
            issuer: issuer.to_owned(),
            audience: audience.to_owned(),
        })
    }

    pub fn verify(&self, token: &str) -> Result<Claims, AuthError> {
        if token.len() > 16 * 1024 {
            return Err(AuthError::InvalidToken);
        }
        let mut parts = token.split('.');
        let encoded_header = parts.next().ok_or(AuthError::InvalidToken)?;
        let encoded_claims = parts.next().ok_or(AuthError::InvalidToken)?;
        let encoded_signature = parts.next().ok_or(AuthError::InvalidToken)?;
        if parts.next().is_some() {
            return Err(AuthError::InvalidToken);
        }
        let header: Header = decode_json(encoded_header)?;
        if header.alg != "HS256" {
            return Err(AuthError::InvalidToken);
        }
        let signature = URL_SAFE_NO_PAD
            .decode(encoded_signature)
            .map_err(|_| AuthError::InvalidToken)?;
        let mut mac =
            Hmac::<Sha256>::new_from_slice(&self.secret).map_err(|_| AuthError::InvalidToken)?;
        mac.update(encoded_header.as_bytes());
        mac.update(b".");
        mac.update(encoded_claims.as_bytes());
        mac.verify_slice(&signature)
            .map_err(|_| AuthError::InvalidToken)?;
        let claims: Claims = decode_json(encoded_claims)?;
        if claims.sub.is_empty()
            || claims.sid.is_empty()
            || claims.iss != self.issuer
            || !claims.aud.contains(&self.audience)
        {
            return Err(AuthError::InvalidToken);
        }
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs();
        if claims.exp <= now {
            return Err(AuthError::SessionExpired);
        }
        if claims.iat > now.saturating_add(60) || claims.iat >= claims.exp {
            return Err(AuthError::InvalidToken);
        }
        Ok(claims)
    }
}

fn decode_json<T: for<'de> Deserialize<'de>>(encoded: &str) -> Result<T, AuthError> {
    let bytes = URL_SAFE_NO_PAD
        .decode(encoded)
        .map_err(|_| AuthError::InvalidToken)?;
    serde_json::from_slice(&bytes).map_err(|_| AuthError::InvalidToken)
}
