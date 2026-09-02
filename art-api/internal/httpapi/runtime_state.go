package httpapi

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type servicePulse struct {
	service     string
	instanceID  string
	onlinePeers int
	receivedAt  time.Time
}

type runtimeState struct {
	mutex        sync.Mutex
	startedAt    time.Time
	pulses       map[string]servicePulse
	lastCPUUsage uint64
	lastCPUAt    time.Time
	history      []infrastructureSample
	commands     []serviceCommand
}

type serviceCommand struct {
	ID             string     `json:"id"`
	Service        string     `json:"service"`
	TargetInstance string     `json:"target_instance"`
	Type           string     `json:"type"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
}

type infrastructureSample struct {
	Timestamp        time.Time `json:"timestamp"`
	CPUPercent       float64   `json:"cpu_percent"`
	MemoryBytes      uint64    `json:"memory_bytes"`
	OnlineDevices    int       `json:"online_devices"`
	ActiveSessions   int       `json:"active_sessions"`
	RelayConnections int       `json:"relay_connections"`
	RelayBandwidth   int64     `json:"relay_bandwidth"`
}

func newRuntimeState() *runtimeState {
	return &runtimeState{startedAt: time.Now().UTC(), pulses: make(map[string]servicePulse)}
}

func (s *runtimeState) heartbeat(service, instanceID string, onlinePeers int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.pulses[service+":"+instanceID] = servicePulse{service: service, instanceID: instanceID,
		onlinePeers: onlinePeers, receivedAt: time.Now().UTC()}
}

func (s *runtimeState) enqueueCommand(service, target, commandType string) serviceCommand {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := time.Now().UTC()
	command := serviceCommand{ID: uuid.NewString(), Service: service, TargetInstance: target, Type: commandType, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	s.commands = append(s.commands, command)
	return command
}

func (s *runtimeState) heartbeatCommands(service, instanceID string, acknowledged []string) []serviceCommand {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := time.Now().UTC()
	ack := make(map[string]bool, len(acknowledged))
	for _, id := range acknowledged {
		ack[id] = true
	}
	result := make([]serviceCommand, 0)
	for index := range s.commands {
		command := &s.commands[index]
		if command.AcknowledgedAt == nil && ack[command.ID] {
			at := now
			command.AcknowledgedAt = &at
			command.AcknowledgedBy = instanceID
		}
		if command.AcknowledgedAt == nil && now.Before(command.ExpiresAt) && command.Service == service && (command.TargetInstance == "*" || command.TargetInstance == instanceID) {
			result = append(result, *command)
		}
	}
	if len(s.commands) > 200 {
		s.commands = append([]serviceCommand(nil), s.commands[len(s.commands)-200:]...)
	}
	return result
}

func (s *runtimeState) listCommands() []serviceCommand {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	// Keep the HTTP contract stable for an empty runtime state. A nil slice is
	// encoded as JSON null, while API consumers correctly expect a collection.
	return append(make([]serviceCommand, 0, len(s.commands)), s.commands...)
}

func (s *runtimeState) service(service string, maxAge time.Duration) (string, int, int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now, instances, peers := time.Now().UTC(), 0, 0
	for key, pulse := range s.pulses {
		if now.Sub(pulse.receivedAt) > 10*maxAge {
			delete(s.pulses, key)
			continue
		}
		if pulse.service == service && now.Sub(pulse.receivedAt) <= maxAge {
			instances++
			peers += pulse.onlinePeers
		}
	}
	if instances == 0 {
		return "offline", 0, 0
	}
	return "online", instances, peers
}

func (s *runtimeState) system() (float64, uint64, uint64, uint64, int64) {
	memoryCharged, _ := readUintFile("/sys/fs/cgroup/memory.current")
	memory := containerMemory()
	limit, limitErr := readUintFile("/sys/fs/cgroup/memory.max")
	if memory == 0 {
		memory = memoryCharged
	}
	if limitErr != nil {
		limit = 0
	}
	usage, usageErr := cpuUsage()
	now := time.Now()
	s.mutex.Lock()
	cpu := 0.0
	if usageErr == nil && s.lastCPUUsage > 0 && usage >= s.lastCPUUsage {
		elapsed := now.Sub(s.lastCPUAt).Seconds()
		if elapsed > 0 {
			cpu = float64(usage-s.lastCPUUsage) / 1_000_000 / elapsed * 100
		}
	}
	if usageErr == nil {
		s.lastCPUUsage, s.lastCPUAt = usage, now
	}
	uptime := int64(time.Since(s.startedAt).Seconds())
	s.mutex.Unlock()
	return cpu, memory, memoryCharged, limit, uptime
}

func (s *runtimeState) record(sample infrastructureSample) []infrastructureSample {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if len(s.history) == 0 || sample.Timestamp.Sub(s.history[len(s.history)-1].Timestamp) >= 8*time.Second {
		s.history = append(s.history, sample)
		const historyLimit = 180
		if len(s.history) > historyLimit {
			copy(s.history, s.history[len(s.history)-historyLimit:])
			s.history = s.history[:historyLimit]
		}
	}
	return append([]infrastructureSample(nil), s.history...)
}

func readUintFile(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

func cpuUsage() (uint64, error) {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			return strconv.ParseUint(fields[1], 10, 64)
		}
	}
	return 0, os.ErrNotExist
}

func containerMemory() uint64 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var pssBytes, rssPages uint64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		if data, err := os.ReadFile("/proc/" + entry.Name() + "/smaps_rollup"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "Pss:" {
					kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
					if parseErr == nil {
						pssBytes += kilobytes * 1024
					}
					break
				}
			}
		}
		data, err := os.ReadFile("/proc/" + entry.Name() + "/statm")
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) < 2 {
			continue
		}
		resident, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			rssPages += resident
		}
	}
	if pssBytes > 0 {
		return pssBytes
	}
	return rssPages * uint64(os.Getpagesize())
}

func logicalCPUs() int { return runtime.NumCPU() }
