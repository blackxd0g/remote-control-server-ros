#[derive(Clone, PartialEq, ::prost::Message)]
pub struct IdPk {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(bytes = "vec", tag = "2")]
    pub pk: Vec<u8>,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct RegisterPeer {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(int32, tag = "2")]
    pub serial: i32,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct RegisterPeerResponse {
    #[prost(bool, tag = "2")]
    pub request_pk: bool,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct RegisterPk {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(bytes = "vec", tag = "2")]
    pub uuid: Vec<u8>,
    #[prost(bytes = "vec", tag = "3")]
    pub pk: Vec<u8>,
    #[prost(string, tag = "4")]
    pub old_id: String,
    #[prost(bool, tag = "5")]
    pub no_register_device: bool,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct RegisterPkResponse {
    #[prost(enumeration = "RegisterPkResult", tag = "1")]
    pub result: i32,
    #[prost(int32, tag = "2")]
    pub keep_alive: i32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, ::prost::Enumeration)]
#[repr(i32)]
pub enum RegisterPkResult {
    Ok = 0,
    UuidMismatch = 2,
    IdExists = 3,
    TooFrequent = 4,
    InvalidIdFormat = 5,
    NotSupport = 6,
    ServerError = 7,
    NotDeployed = 8,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct PunchHoleRequest {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(enumeration = "NatType", tag = "2")]
    pub nat_type: i32,
    #[prost(string, tag = "3")]
    pub licence_key: String,
    #[prost(enumeration = "ConnType", tag = "4")]
    pub conn_type: i32,
    #[prost(string, tag = "5")]
    pub token: String,
    #[prost(string, tag = "6")]
    pub version: String,
    #[prost(int32, tag = "7")]
    pub udp_port: i32,
    #[prost(bool, tag = "8")]
    pub force_relay: bool,
    #[prost(int32, tag = "9")]
    pub upnp_port: i32,
    #[prost(bytes = "vec", tag = "10")]
    pub socket_addr_v6: Vec<u8>,
    #[prost(string, tag = "11")]
    pub switch_code: String,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct RequestRelay {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(string, tag = "2")]
    pub uuid: String,
    #[prost(bytes = "vec", tag = "3")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "4")]
    pub relay_server: String,
    #[prost(bool, tag = "5")]
    pub secure: bool,
    #[prost(string, tag = "6")]
    pub licence_key: String,
    #[prost(enumeration = "ConnType", tag = "7")]
    pub conn_type: i32,
    #[prost(string, tag = "8")]
    pub token: String,
    #[prost(string, tag = "11")]
    pub switch_code: String,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct PunchHoleResponse {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(bytes = "vec", tag = "2")]
    pub pk: Vec<u8>,
    #[prost(enumeration = "PunchFailure", tag = "3")]
    pub failure: i32,
    #[prost(string, tag = "4")]
    pub relay_server: String,
    #[prost(oneof = "punch_hole_response::Union", tags = "5, 6")]
    pub union: Option<punch_hole_response::Union>,
    #[prost(string, tag = "7")]
    pub other_failure: String,
    #[prost(int32, tag = "8")]
    pub feedback: i32,
    #[prost(bool, tag = "9")]
    pub is_udp: bool,
    #[prost(int32, tag = "10")]
    pub upnp_port: i32,
    #[prost(bytes = "vec", tag = "11")]
    pub socket_addr_v6: Vec<u8>,
}

pub mod punch_hole_response {
    #[derive(Clone, PartialEq, ::prost::Oneof)]
    pub enum Union {
        #[prost(enumeration = "super::NatType", tag = "5")]
        NatType(i32),
        #[prost(bool, tag = "6")]
        IsLocal(bool),
    }
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct PunchHole {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "2")]
    pub relay_server: String,
    #[prost(enumeration = "NatType", tag = "3")]
    pub nat_type: i32,
    #[prost(int32, tag = "4")]
    pub udp_port: i32,
    #[prost(bool, tag = "5")]
    pub force_relay: bool,
    #[prost(int32, tag = "6")]
    pub upnp_port: i32,
    #[prost(bytes = "vec", tag = "7")]
    pub socket_addr_v6: Vec<u8>,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct RelayResponse {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "2")]
    pub uuid: String,
    #[prost(string, tag = "3")]
    pub relay_server: String,
    #[prost(oneof = "relay_response::Union", tags = "4, 5")]
    pub union: Option<relay_response::Union>,
    #[prost(string, tag = "6")]
    pub refuse_reason: String,
    #[prost(string, tag = "7")]
    pub version: String,
    #[prost(int32, tag = "9")]
    pub feedback: i32,
}

pub mod relay_response {
    #[derive(Clone, PartialEq, ::prost::Oneof)]
    pub enum Union {
        #[prost(string, tag = "4")]
        Id(String),
        #[prost(bytes = "vec", tag = "5")]
        Pk(Vec<u8>),
    }
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct KeyExchange {
    #[prost(bytes = "vec", repeated, tag = "1")]
    pub keys: Vec<Vec<u8>>,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct TestNatRequest {
    #[prost(int32, tag = "1")]
    pub serial: i32,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct TestNatResponse {
    #[prost(int32, tag = "1")]
    pub port: i32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, ::prost::Enumeration)]
#[repr(i32)]
pub enum ConnType {
    DefaultConn = 0,
    FileTransfer = 1,
    PortForward = 2,
    Rdp = 3,
    ViewCamera = 4,
    Terminal = 5,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, ::prost::Enumeration)]
#[repr(i32)]
pub enum NatType {
    UnknownNat = 0,
    Asymmetric = 1,
    Symmetric = 2,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, ::prost::Enumeration)]
#[repr(i32)]
pub enum PunchFailure {
    IdNotExist = 0,
    Offline = 2,
    LicenseMismatch = 3,
    LicenseOveruse = 4,
}

#[derive(Clone, PartialEq, ::prost::Message)]
pub struct RendezvousMessage {
    #[prost(
        oneof = "rendezvous_message::Union",
        tags = "6, 7, 8, 9, 11, 15, 16, 18, 19, 20, 21, 25"
    )]
    pub union: Option<rendezvous_message::Union>,
}

pub mod rendezvous_message {
    #[derive(Clone, PartialEq, ::prost::Oneof)]
    pub enum Union {
        #[prost(message, tag = "6")]
        RegisterPeer(super::RegisterPeer),
        #[prost(message, tag = "7")]
        RegisterPeerResponse(super::RegisterPeerResponse),
        #[prost(message, tag = "8")]
        PunchHoleRequest(super::PunchHoleRequest),
        #[prost(message, tag = "9")]
        PunchHole(super::PunchHole),
        #[prost(message, tag = "11")]
        PunchHoleResponse(super::PunchHoleResponse),
        #[prost(message, tag = "15")]
        RegisterPk(super::RegisterPk),
        #[prost(message, tag = "16")]
        RegisterPkResponse(super::RegisterPkResponse),
        #[prost(message, tag = "18")]
        RequestRelay(super::RequestRelay),
        #[prost(message, tag = "19")]
        RelayResponse(super::RelayResponse),
        #[prost(message, tag = "20")]
        TestNatRequest(super::TestNatRequest),
        #[prost(message, tag = "21")]
        TestNatResponse(super::TestNatResponse),
        #[prost(message, tag = "25")]
        KeyExchange(super::KeyExchange),
    }
}
