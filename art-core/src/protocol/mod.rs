mod codec;
mod messages;

pub use codec::BytesCodec;
pub use messages::*;

pub fn encode_socket_addr(address: std::net::SocketAddr) -> Vec<u8> {
    use std::time::{SystemTime, UNIX_EPOCH};
    let address = match address {
        std::net::SocketAddr::V6(value) if !value.ip().is_loopback() => value
            .ip()
            .to_ipv4()
            .map(|ip| std::net::SocketAddr::new(ip.into(), value.port()))
            .unwrap_or(std::net::SocketAddr::V6(value)),
        value => value,
    };
    match address {
        std::net::SocketAddr::V4(value) => {
            let salt = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_micros() as u32 as u128;
            let ip = u32::from_le_bytes(value.ip().octets()) as u128;
            let port = u128::from(value.port());
            let packed = ((ip + salt) << 49) | (salt << 17) | (port + (salt & 0xffff));
            let bytes = packed.to_le_bytes();
            let length = bytes
                .iter()
                .rposition(|byte| *byte != 0)
                .map_or(0, |index| index + 1);
            bytes[..length].to_vec()
        }
        std::net::SocketAddr::V6(value) => {
            let mut bytes = value.ip().octets().to_vec();
            bytes.extend_from_slice(&value.port().to_le_bytes());
            bytes
        }
    }
}
