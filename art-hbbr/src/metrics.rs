use std::{
    pin::Pin,
    sync::{
        Arc,
        atomic::{AtomicU64, AtomicUsize, Ordering},
    },
    task::{Context, Poll},
};

use tokio::{
    io::{AsyncRead, AsyncWrite, ReadBuf},
    net::TcpStream,
};

#[derive(Default)]
pub struct RelayMetrics {
    active: AtomicUsize,
    bytes: Arc<AtomicU64>,
}

impl RelayMetrics {
    pub fn start(self: &Arc<Self>) -> ActiveRelay {
        self.active.fetch_add(1, Ordering::Relaxed);
        ActiveRelay {
            metrics: self.clone(),
        }
    }

    pub fn connections(&self) -> usize {
        self.active.load(Ordering::Relaxed)
    }

    pub fn total_bytes(&self) -> u64 {
        self.bytes.load(Ordering::Relaxed)
    }

    pub fn meter(&self, stream: TcpStream) -> MeteredStream {
        MeteredStream {
            stream,
            bytes: self.bytes.clone(),
        }
    }

    pub fn record_bytes(&self, count: usize) {
        self.bytes
            .fetch_add(u64::try_from(count).unwrap_or(u64::MAX), Ordering::Relaxed);
    }
}

pub struct ActiveRelay {
    metrics: Arc<RelayMetrics>,
}

impl Drop for ActiveRelay {
    fn drop(&mut self) {
        self.metrics.active.fetch_sub(1, Ordering::Relaxed);
    }
}

pub struct MeteredStream {
    stream: TcpStream,
    bytes: Arc<AtomicU64>,
}

impl AsyncRead for MeteredStream {
    fn poll_read(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffer: &mut ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        let before = buffer.filled().len();
        let result = Pin::new(&mut self.stream).poll_read(context, buffer);
        if let Poll::Ready(Ok(())) = &result {
            self.bytes.fetch_add(
                u64::try_from(buffer.filled().len() - before).unwrap_or(u64::MAX),
                Ordering::Relaxed,
            );
        }
        result
    }
}

impl AsyncWrite for MeteredStream {
    fn poll_write(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffer: &[u8],
    ) -> Poll<std::io::Result<usize>> {
        Pin::new(&mut self.stream).poll_write(context, buffer)
    }

    fn poll_flush(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.stream).poll_flush(context)
    }

    fn poll_shutdown(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<std::io::Result<()>> {
        Pin::new(&mut self.stream).poll_shutdown(context)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn active_relay_guard_tracks_connections() {
        let metrics = Arc::new(RelayMetrics::default());
        let first = metrics.start();
        assert_eq!(metrics.connections(), 1);
        {
            let _second = metrics.start();
            assert_eq!(metrics.connections(), 2);
        }
        assert_eq!(metrics.connections(), 1);
        drop(first);
        assert_eq!(metrics.connections(), 0);
    }
}
