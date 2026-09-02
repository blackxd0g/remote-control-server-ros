use std::io;

use bytes::{Buf, BufMut, Bytes, BytesMut};
use tokio_util::codec::{Decoder, Encoder};

const MAX_PREALLOCATED: usize = 256 * 1024;

#[derive(Clone, Copy, Debug)]
pub struct BytesCodec {
    expected: Option<usize>,
    max_packet_length: usize,
}

impl BytesCodec {
    pub fn new(max_packet_length: usize) -> Self {
        Self {
            expected: None,
            max_packet_length,
        }
    }
}

impl Decoder for BytesCodec {
    type Item = BytesMut;
    type Error = io::Error;

    fn decode(&mut self, source: &mut BytesMut) -> io::Result<Option<Self::Item>> {
        if self.expected.is_none() {
            if source.is_empty() {
                return Ok(None);
            }
            let header_length = usize::from((source[0] & 3) + 1);
            if source.len() < header_length {
                return Ok(None);
            }
            let mut length = 0usize;
            for (index, byte) in source[..header_length].iter().enumerate() {
                length |= usize::from(*byte) << (index * 8);
            }
            length >>= 2;
            if length > self.max_packet_length {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    "frame exceeds packet limit",
                ));
            }
            source.advance(header_length);
            source.reserve(length.saturating_sub(source.len()).min(MAX_PREALLOCATED));
            self.expected = Some(length);
        }
        let length = self.expected.expect("length was assigned");
        if source.len() < length {
            return Ok(None);
        }
        self.expected = None;
        Ok(Some(source.split_to(length)))
    }
}

impl Encoder<Bytes> for BytesCodec {
    type Error = io::Error;

    fn encode(&mut self, data: Bytes, target: &mut BytesMut) -> io::Result<()> {
        let length = data.len();
        if length > self.max_packet_length {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "frame exceeds packet limit",
            ));
        }
        if length <= 0x3f {
            target.put_u8((length << 2) as u8);
        } else if length <= 0x3fff {
            target.put_u16_le(((length << 2) | 1) as u16);
        } else if length <= 0x3f_ffff {
            let header = ((length << 2) | 2) as u32;
            target.put_u16_le((header & 0xffff) as u16);
            target.put_u8((header >> 16) as u8);
        } else if length <= 0x3fff_ffff {
            target.put_u32_le(((length << 2) | 3) as u32);
        } else {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "frame length overflow",
            ));
        }
        target.extend_from_slice(&data);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn round_trip_all_header_sizes() {
        for length in [0, 63, 64, 16_383, 16_384, 100_000] {
            let mut codec = BytesCodec::new(200_000);
            let input = Bytes::from(vec![7; length]);
            let mut encoded = BytesMut::new();
            codec.encode(input.clone(), &mut encoded).unwrap();
            assert_eq!(codec.decode(&mut encoded).unwrap().unwrap(), input);
        }
    }

    #[test]
    fn rejects_large_untrusted_header_before_allocation() {
        let mut codec = BytesCodec::new(1024);
        let mut encoded = BytesMut::new();
        encoded.put_u32_le(((2048usize << 2) | 3) as u32);
        assert!(codec.decode(&mut encoded).is_err());
    }
}
