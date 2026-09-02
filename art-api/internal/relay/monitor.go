package relay

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
)

type Monitor struct {
	repository domain.Repository
	interval   time.Duration
	timeout    time.Duration
	hub        *events.Hub
}

func NewMonitor(repository domain.Repository, hub *events.Hub, interval, timeout time.Duration) *Monitor {
	return &Monitor{repository: repository, hub: hub, interval: interval, timeout: timeout}
}

func (m *Monitor) Run(ctx context.Context) {
	m.check(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

func (m *Monitor) check(ctx context.Context) {
	values, err := m.repository.ListRelayServers(ctx)
	if err != nil {
		slog.Warn("relay health list failed", "error", err)
		return
	}
	for _, value := range values {
		if !value.Enabled {
			continue
		}
		started := time.Now()
		connection, dialErr := (&net.Dialer{Timeout: m.timeout}).DialContext(ctx, "tcp", net.JoinHostPort(value.Hostname, strconv.Itoa(value.Port)))
		health, latency := "offline", 0
		if dialErr == nil {
			health, latency = "healthy", int(time.Since(started).Milliseconds())
			_ = connection.Close()
		}
		if err := m.repository.UpdateRelayHealth(ctx, value.ID, health, latency, time.Now().UTC()); err != nil {
			slog.Warn("relay health update failed", "relay_id", value.ID, "error", err)
		} else if value.Health != health || value.LatencyMS != latency {
			value.Health, value.LatencyMS = health, latency
			m.hub.Publish(events.RelayUpdated, value)
		}
		now := time.Now().UTC()
		_ = m.repository.AppendRelayMetric(ctx, domain.RelayMetric{RelayID: value.ID, RecordedAt: now, Health: health, LatencyMS: latency, Connections: value.Connections, Bandwidth: value.Bandwidth})
	}
	_ = m.repository.PruneRelayMetrics(ctx, time.Now().UTC().Add(-30*24*time.Hour))
}

func Select(values []domain.RelayServer, region string) (domain.RelayServer, error) {
	var best domain.RelayServer
	for _, value := range values {
		if !value.Enabled || value.Health != "healthy" || (region != "" && value.Region != region) {
			continue
		}
		if best.ID == "" || value.Connections < best.Connections || (value.Connections == best.Connections && value.LatencyMS < best.LatencyMS) {
			best = value
		}
	}
	if best.ID == "" {
		return best, domain.ErrNotFound
	}
	return best, nil
}
