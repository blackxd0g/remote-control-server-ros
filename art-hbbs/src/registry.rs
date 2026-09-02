use std::{collections::HashMap, net::SocketAddr, sync::RwLock, time::Instant};

#[derive(Clone)]
pub struct Peer {
    pub address: SocketAddr,
    pub public_key: Vec<u8>,
    pub last_seen: Instant,
}

#[derive(Default)]
pub struct PeerRegistry {
    peers: RwLock<HashMap<String, Peer>>,
}

impl PeerRegistry {
    pub fn register(&self, id: String, address: SocketAddr, public_key: Option<Vec<u8>>) {
        if let Ok(mut peers) = self.peers.write() {
            let existing_key = peers
                .get(&id)
                .map(|peer| peer.public_key.clone())
                .unwrap_or_default();
            peers.insert(
                id,
                Peer {
                    address,
                    public_key: public_key.unwrap_or(existing_key),
                    last_seen: Instant::now(),
                },
            );
        }
    }

    pub fn get(&self, id: &str) -> Option<Peer> {
        self.peers
            .read()
            .ok()?
            .get(id)
            .filter(|peer| peer.last_seen.elapsed().as_secs() < 180)
            .cloned()
    }

    pub fn active_count(&self) -> usize {
        self.peers
            .read()
            .map(|peers| {
                peers
                    .values()
                    .filter(|peer| peer.last_seen.elapsed().as_secs() < 180)
                    .count()
            })
            .unwrap_or(0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn active_count_tracks_registered_peers() {
        let registry = PeerRegistry::default();
        registry.register("123456789".into(), "127.0.0.1:21116".parse().unwrap(), None);
        assert_eq!(registry.active_count(), 1);
    }
}
