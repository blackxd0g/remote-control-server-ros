use serde::{Deserialize, Serialize};
use std::{
    collections::{HashSet, VecDeque},
    fs::{self, File, OpenOptions},
    io::Write,
    path::PathBuf,
};

const CAPACITY: usize = 4096;
const MAX_BYTES: u64 = 2 * 1024 * 1024;

#[derive(Clone, Default, Serialize, Deserialize)]
pub(crate) struct Event {
    pub id: String,
    pub uuid: String,
    pub status: String,
    #[serde(default)]
    pub created_at: u64,
    #[serde(default)]
    pub reason: String,
}

#[derive(Clone, Default, Serialize, Deserialize)]
pub(crate) struct DeliveryStatus {
    pub pending: usize,
    pub quarantined: usize,
    pub reserved: usize,
    pub capacity: usize,
    pub healthy: bool,
    pub blocked: bool,
    pub oldest_at: u64,
    pub sample: Vec<Event>,
}
fn now_millis() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}

#[derive(Default, Serialize, Deserialize)]
struct Snapshot {
    version: u32,
    events: VecDeque<Event>,
    active: HashSet<String>,
    #[serde(default)]
    quarantine: Vec<Event>,
    #[serde(default)]
    quarantine_revision: String,
    #[serde(default)]
    last_archive: String,
}

pub(crate) struct Outbox {
    dir: PathBuf,
    snapshot: Snapshot,
    healthy: bool,
    _lock: File,
}

impl Outbox {
    pub fn open(dir: PathBuf) -> anyhow::Result<Self> {
        fs::create_dir_all(&dir)?;
        let lock = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(dir.join("owner.lock"))?;
        lock.try_lock()
            .map_err(|_| anyhow::anyhow!("relay outbox is already in use"))?;
        let path = dir.join("outbox.json");
        let mut snapshot = if path.exists() {
            anyhow::ensure!(
                fs::metadata(&path)?.len() <= MAX_BYTES,
                "outbox file too large"
            );
            serde_json::from_slice::<Snapshot>(&fs::read(path)?)?
        } else {
            Snapshot {
                version: 2,
                ..Default::default()
            }
        };
        anyhow::ensure!(
            snapshot.version == 1 || snapshot.version == 2,
            "unsupported outbox version"
        );
        snapshot.version = 2;
        if snapshot.quarantine_revision.is_empty() {
            snapshot.quarantine_revision = uuid::Uuid::new_v4().to_string();
        }
        anyhow::ensure!(
            snapshot.events.len() + snapshot.active.len() + snapshot.quarantine.len() <= CAPACITY,
            "outbox capacity exceeded"
        );
        let mut identities = HashSet::new();
        for e in snapshot.events.iter().chain(snapshot.quarantine.iter()) {
            anyhow::ensure!(
                uuid::Uuid::parse_str(&e.id).is_ok()
                    && identities.insert(e.id.clone())
                    && (8..=128).contains(&e.uuid.len())
                    && (e.status == "active" || e.status == "closed"),
                "invalid outbox event"
            );
        }
        anyhow::ensure!(
            snapshot
                .quarantine
                .iter()
                .all(|e| e.reason == "credential_rotated"),
            "invalid quarantine reason"
        );
        for uuid in snapshot.active.drain() {
            anyhow::ensure!((8..=128).contains(&uuid.len()), "invalid active UUID");
            snapshot.events.push_back(Event {
                id: uuid::Uuid::new_v4().to_string(),
                uuid,
                status: "closed".into(),
                created_at: now_millis(),
                ..Default::default()
            });
        }
        let mut value = Self {
            dir,
            snapshot,
            healthy: false,
            _lock: lock,
        };
        value.flush()?;
        Ok(value)
    }
    pub fn healthy(&self) -> bool {
        self.healthy
    }
    pub fn can_admit(&self) -> bool {
        self.healthy
            && self.snapshot.events.len()
                + self.snapshot.active.len()
                + self.snapshot.quarantine.len()
                + 2
                <= CAPACITY
    }
    pub fn flush(&mut self) -> anyhow::Result<()> {
        self.healthy = false;
        let bytes = serde_json::to_vec(&self.snapshot)?;
        anyhow::ensure!(bytes.len() as u64 <= MAX_BYTES, "outbox file too large");
        let mut options = OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut file = options.open(self.dir.join("outbox.tmp"))?;
        file.write_all(&bytes)?;
        file.sync_all()?;
        drop(file);
        fs::rename(self.dir.join("outbox.tmp"), self.dir.join("outbox.json"))?;
        #[cfg(unix)]
        File::open(&self.dir)?.sync_all()?;
        self.healthy = true;
        Ok(())
    }
    pub fn report(&mut self, uuid: &str, status: &str) -> anyhow::Result<()> {
        anyhow::ensure!((8..=128).contains(&uuid.len()), "invalid lifecycle UUID");
        match status {
            "active" => {
                anyhow::ensure!(self.healthy, "outbox storage unavailable");
                anyhow::ensure!(
                    !self.snapshot.active.contains(uuid),
                    "duplicate active connection"
                );
                // Reserve a slot for closed before admitting a new data pair.
                anyhow::ensure!(
                    self.snapshot.events.len()
                        + self.snapshot.active.len()
                        + self.snapshot.quarantine.len()
                        + 2
                        <= CAPACITY,
                    "outbox full"
                );
                self.snapshot.active.insert(uuid.into());
            }
            "closed" => {
                if !self.snapshot.active.remove(uuid) {
                    return Ok(());
                }
            }
            _ => anyhow::bail!("invalid lifecycle state"),
        }
        self.snapshot.events.push_back(Event {
            id: uuid::Uuid::new_v4().to_string(),
            uuid: uuid.into(),
            status: status.into(),
            created_at: now_millis(),
            ..Default::default()
        });
        // Keep changed memory on disk failure. Admission stops until a successful flush.
        self.flush()
    }
    pub fn front(&self) -> Option<Event> {
        self.snapshot.events.front().cloned()
    }
    pub fn status(&self) -> DeliveryStatus {
        DeliveryStatus {
            pending: self.snapshot.events.len(),
            quarantined: self.snapshot.quarantine.len(),
            reserved: self.snapshot.active.len(),
            capacity: CAPACITY,
            healthy: self.healthy,
            blocked: !self.can_admit(),
            oldest_at: self.snapshot.events.front().map_or(0, |e| e.created_at),
            sample: self.snapshot.quarantine.iter().take(5).cloned().collect(),
        }
    }
    pub fn quarantine(
        &mut self,
        id: &str,
        uuid: &str,
        status: &str,
        reason: &str,
    ) -> anyhow::Result<()> {
        anyhow::ensure!(
            reason == "credential_rotated",
            "unsupported quarantine reason"
        );
        if let Some(front) = self.snapshot.events.front()
            && front.id == id
        {
            anyhow::ensure!(
                front.uuid == uuid && front.status == status,
                "quarantine identity mismatch"
            );
            let mut event = self.snapshot.events.pop_front().expect("front exists");
            event.reason = reason.into();
            self.snapshot.quarantine.push(event);
            self.snapshot.quarantine_revision = uuid::Uuid::new_v4().to_string();
            self.flush()?;
        }
        Ok(())
    }
    pub fn acknowledge(&mut self, id: &str, uuid: &str, status: &str) -> anyhow::Result<()> {
        if let Some(front) = self.snapshot.events.front()
            && front.id == id
        {
            anyhow::ensure!(
                front.uuid == uuid && front.status == status,
                "outbox acknowledgement mismatch"
            );
            self.snapshot.events.pop_front();
            self.flush()?;
        }
        Ok(())
    }

    pub fn quarantine_page(&self, revision: &str, offset: usize) -> anyhow::Result<QuarantinePage> {
        anyhow::ensure!(
            revision.is_empty() || revision == self.snapshot.quarantine_revision,
            "quarantine changed"
        );
        anyhow::ensure!(offset <= self.snapshot.quarantine.len(), "invalid offset");
        Ok(QuarantinePage {
            revision: self.snapshot.quarantine_revision.clone(),
            total: self.snapshot.quarantine.len(),
            offset,
            events: self
                .snapshot
                .quarantine
                .iter()
                .skip(offset)
                .take(10)
                .cloned()
                .collect(),
        })
    }

    pub fn archive_quarantine(&mut self, revision: &str) -> anyhow::Result<()> {
        anyhow::ensure!(uuid::Uuid::parse_str(revision).is_ok(), "invalid revision");
        if self.snapshot.last_archive == revision {
            return self.flush(); // A lost acknowledgement never clears a newer quarantine.
        }
        anyhow::ensure!(
            self.healthy && revision == self.snapshot.quarantine_revision,
            "quarantine changed or storage unavailable"
        );
        anyhow::ensure!(!self.snapshot.quarantine.is_empty(), "quarantine empty");
        let bytes = serde_json::to_vec(&QuarantinePage {
            revision: revision.into(),
            total: self.snapshot.quarantine.len(),
            offset: 0,
            events: self.snapshot.quarantine.clone(),
        })?;
        let path = self.dir.join(format!("quarantine-{revision}.json"));
        // Exclusive creation, no overwrite. An incomplete backup prevents deletion.
        if path.exists() {
            anyhow::ensure!(fs::read(&path)? == bytes, "backup mismatch");
            File::open(&path)?.sync_all()?;
        } else {
            let mut options = OpenOptions::new();
            options.write(true).create_new(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::OpenOptionsExt;
                options.mode(0o600);
            }
            let mut file = options.open(&path)?;
            file.write_all(&bytes)?;
            file.sync_all()?;
        }
        #[cfg(unix)]
        File::open(&self.dir)?.sync_all()?;
        self.snapshot.quarantine.clear();
        self.snapshot.last_archive = revision.into();
        self.snapshot.quarantine_revision = uuid::Uuid::new_v4().to_string();
        self.flush()
    }
}

#[derive(Clone, Default, Serialize, Deserialize)]
pub(crate) struct QuarantinePage {
    pub revision: String,
    pub total: usize,
    pub offset: usize,
    pub events: Vec<Event>,
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn archive_is_revision_guarded_backed_up_and_retry_safe_after_restart() {
        let dir = Temp::new();
        let mut q = Outbox::open(dir.0.clone()).unwrap();
        q.report("archive-test-uuid", "active").unwrap();
        let e = q.front().unwrap();
        q.quarantine(&e.id, &e.uuid, &e.status, "credential_rotated")
            .unwrap();
        let page = q.quarantine_page("", 0).unwrap();
        q.report("archive-test-uuid", "closed").unwrap();
        let closed = q.front().unwrap();
        q.quarantine(
            &closed.id,
            &closed.uuid,
            &closed.status,
            "credential_rotated",
        )
        .unwrap();
        assert!(q.archive_quarantine(&page.revision).is_err());
        let page = q.quarantine_page("", 0).unwrap();
        // A failed/partial backup never removes queue data.
        let backup = dir.0.join(format!("quarantine-{}.json", page.revision));
        fs::write(&backup, b"partial").unwrap();
        assert!(q.archive_quarantine(&page.revision).is_err());
        assert_eq!(q.status().quarantined, 2);
        fs::remove_file(&backup).unwrap();
        q.archive_quarantine(&page.revision).unwrap();
        let saved: QuarantinePage = serde_json::from_slice(&fs::read(&backup).unwrap()).unwrap();
        assert_eq!(saved.events.len(), 2);
        assert_eq!(saved.events[0].id, e.id);
        drop(q);
        let mut q = Outbox::open(dir.0.clone()).unwrap();
        q.report("another-test-uuid", "active").unwrap();
        let e = q.front().unwrap();
        q.quarantine(&e.id, &e.uuid, &e.status, "credential_rotated")
            .unwrap();
        q.archive_quarantine(&page.revision).unwrap();
        assert_eq!(q.status().quarantined, 1);
        assert!(q.archive_quarantine("../../escape").is_err());
    }

    #[test]
    fn full_quarantine_archive_restores_admission_and_pages_are_bounded() {
        let dir = Temp::new();
        let mut q = Outbox::open(dir.0.clone()).unwrap();
        q.snapshot.quarantine = (0..CAPACITY)
            .map(|_| Event {
                id: uuid::Uuid::new_v4().to_string(),
                uuid: "capacity-test-uuid".into(),
                status: "closed".into(),
                reason: "credential_rotated".into(),
                ..Default::default()
            })
            .collect();
        q.flush().unwrap();
        assert!(!q.can_admit());
        let page = q.quarantine_page("", 0).unwrap();
        assert_eq!(page.events.len(), 10);
        assert_eq!(
            q.quarantine_page(&page.revision, 4090)
                .unwrap()
                .events
                .len(),
            6
        );
        drop(q);
        let mut q = Outbox::open(dir.0.clone()).unwrap();
        assert_eq!(q.status().quarantined, 4096);
        q.archive_quarantine(&page.revision).unwrap();
        assert!(q.can_admit());
        q.report("restored-test-uuid", "active").unwrap();
    }

    #[test]
    fn archive_snapshot_failure_recovers_from_old_snapshot_and_existing_backup() {
        let dir = Temp::new();
        let mut q = Outbox::open(dir.0.clone()).unwrap();
        q.report("disk-fail-uuid", "active").unwrap();
        let e = q.front().unwrap();
        q.quarantine(&e.id, &e.uuid, &e.status, "credential_rotated")
            .unwrap();
        let page = q.quarantine_page("", 0).unwrap();
        fs::create_dir(dir.0.join("outbox.tmp")).unwrap();
        assert!(q.archive_quarantine(&page.revision).is_err());
        assert!(!q.can_admit());
        assert!(
            dir.0
                .join(format!("quarantine-{}.json", page.revision))
                .exists()
        );
        drop(q);
        fs::remove_dir(dir.0.join("outbox.tmp")).unwrap();
        let mut q = Outbox::open(dir.0.clone()).unwrap();
        assert_eq!(q.status().quarantined, 1);
        q.archive_quarantine(&page.revision).unwrap();
        assert_eq!(q.status().quarantined, 0);
    }
    struct Temp(PathBuf);
    impl Temp {
        fn new() -> Self {
            Self(std::env::temp_dir().join(format!("relay-outbox-{}", uuid::Uuid::new_v4())))
        }
    }
    impl Drop for Temp {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }
    #[test]
    fn restart_replays_same_event_and_closes_interrupted_pair() {
        let dir = Temp::new();
        let mut queue = Outbox::open(dir.0.clone()).unwrap();
        queue.report("relay-uuid-1", "active").unwrap();
        let first = queue.front().unwrap();
        drop(queue);
        let mut queue = Outbox::open(dir.0.clone()).unwrap();
        assert_eq!(queue.front().unwrap().id, first.id);
        assert!(
            queue
                .acknowledge(&first.id, "foreign-uuid", "active")
                .is_err()
        );
        queue
            .acknowledge(&first.id, &first.uuid, &first.status)
            .unwrap();
        let closed = queue.front().unwrap();
        assert_eq!(closed.status, "closed");
        drop(queue);
        let mut queue = Outbox::open(dir.0.clone()).unwrap();
        assert_eq!(queue.front().unwrap().id, closed.id);
        queue
            .acknowledge(&closed.id, &closed.uuid, &closed.status)
            .unwrap();
        drop(queue);
        assert!(Outbox::open(dir.0.clone()).unwrap().front().is_none());
    }
    #[test]
    fn corrupted_disk_and_second_owner_fail_closed() {
        let dir = Temp::new();
        let queue = Outbox::open(dir.0.clone()).unwrap();
        assert!(Outbox::open(dir.0.clone()).is_err());
        drop(queue);
        fs::write(dir.0.join("outbox.json"), b"broken").unwrap();
        assert!(Outbox::open(dir.0.clone()).is_err());
    }
    #[test]
    fn disk_failure_retains_memory_and_recovery_flushes_it() {
        let dir = Temp::new();
        let mut queue = Outbox::open(dir.0.clone()).unwrap();
        fs::create_dir(dir.0.join("outbox.tmp")).unwrap();
        assert!(queue.report("relay-uuid-1", "active").is_err());
        assert!(!queue.healthy());
        assert!(queue.front().is_some());
        assert!(queue.report("relay-uuid-2", "active").is_err());
        fs::remove_dir(dir.0.join("outbox.tmp")).unwrap();
        queue.flush().unwrap();
        assert!(queue.healthy());
        drop(queue);
        assert!(Outbox::open(dir.0.clone()).unwrap().front().is_some());
    }
    #[test]
    fn capacity_reserves_room_for_close() {
        let dir = Temp::new();
        let mut queue = Outbox::open(dir.0.clone()).unwrap();
        // Fill the persisted model directly to avoid thousands of fsyncs in a unit test.
        for i in 0..CAPACITY - 2 {
            queue.snapshot.events.push_back(Event {
                id: uuid::Uuid::new_v4().to_string(),
                uuid: format!("relay-uuid-{i}"),
                status: "closed".into(),
                ..Default::default()
            });
        }
        queue.report("last-relay-uuid", "active").unwrap();
        assert!(queue.report("extra-relay-uuid", "active").is_err());
        queue.report("last-relay-uuid", "closed").unwrap();
        assert_eq!(queue.snapshot.events.len(), CAPACITY);
    }
    #[test]
    fn quarantine_survives_restart_and_unblocks_following_events() {
        let dir = Temp::new();
        let mut queue = Outbox::open(dir.0.clone()).unwrap();
        queue.report("rotated-relay-uuid", "active").unwrap();
        queue.report("rotated-relay-uuid", "closed").unwrap();
        let first = queue.front().unwrap();
        assert!(
            queue
                .quarantine(&first.id, "foreign-uuid", "active", "credential_rotated")
                .is_err()
        );
        queue
            .quarantine(&first.id, &first.uuid, &first.status, "credential_rotated")
            .unwrap();
        let next = queue.front().unwrap();
        assert_eq!(next.status, "closed");
        queue
            .quarantine(&next.id, &next.uuid, &next.status, "credential_rotated")
            .unwrap();
        queue.report("fresh-relay-uuid", "active").unwrap();
        assert_eq!(queue.status().quarantined, 2);
        assert_eq!(queue.status().pending, 1);
        assert!(!queue.status().blocked);
        drop(queue);
        let queue = Outbox::open(dir.0.clone()).unwrap();
        assert_eq!(queue.status().quarantined, 2);
        assert_eq!(queue.front().unwrap().uuid, "fresh-relay-uuid");
        assert_eq!(queue.status().sample[0].id, first.id);
    }
    #[test]
    fn version_one_migrates_without_losing_pending_event() {
        let dir = Temp::new();
        fs::create_dir_all(&dir.0).unwrap();
        let id = uuid::Uuid::new_v4().to_string();
        let bytes = serde_json::json!({"version":1,"events":[{"id":id,"uuid":"old-relay-uuid","status":"closed"}],"active":[]});
        fs::write(
            dir.0.join("outbox.json"),
            serde_json::to_vec(&bytes).unwrap(),
        )
        .unwrap();
        let queue = Outbox::open(dir.0.clone()).unwrap();
        assert_eq!(queue.snapshot.version, 2);
        assert_eq!(queue.front().unwrap().id, id);
        assert_eq!(queue.status().oldest_at, 0);
    }
}
