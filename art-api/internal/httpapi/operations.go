package httpapi

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type diagnosticCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (s *Server) diagnostics(response http.ResponseWriter, request *http.Request) {
	checks := make([]diagnosticCheck, 0, 8)
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	if _, err := s.repository.CountUsers(ctx); err != nil {
		checks = append(checks, diagnosticCheck{Name: "database", Status: "error", Message: "database query failed"})
	} else {
		checks = append(checks, diagnosticCheck{Name: "database", Status: "ok", Message: "database is responsive"})
	}

	dataDir := filepath.Dir(s.brandingDir)
	if dataDir == "." || dataDir == "" {
		dataDir = envValue("ART_DATA_DIR", "/data")
	}
	if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
		checks = append(checks, diagnosticCheck{Name: "data_directory", Status: "error", Message: "persistent data directory is unavailable"})
	} else {
		checks = append(checks, diagnosticCheck{Name: "data_directory", Status: "ok", Message: dataDir})
	}
	for name, path := range map[string]string{
		"jwt_secret":      envValue("ART_JWT_SECRET_FILE", filepath.Join(dataDir, "secrets", "jwt.secret")),
		"internal_secret": envValue("ART_INTERNAL_SECRET_FILE", filepath.Join(dataDir, "secrets", "internal.secret")),
		"server_key":      envValue("ART_SERVER_KEY_FILE", filepath.Join(dataDir, "secrets", "id_ed25519")),
	} {
		checks = append(checks, secretDiagnostic(name, path))
	}
	hbbs, hbbsInstances, _ := s.runtime.service("hbbs", 20*time.Second)
	hbbr, hbbrInstances, _ := s.runtime.service("hbbr", 20*time.Second)
	checks = append(checks,
		diagnosticCheck{Name: "hbbs", Status: serviceDiagnosticStatus(hbbs), Message: fmt.Sprintf("%s (%d instances)", hbbs, hbbsInstances)},
		diagnosticCheck{Name: "hbbr", Status: serviceDiagnosticStatus(hbbr), Message: fmt.Sprintf("%s (%d instances)", hbbr, hbbrInstances)},
	)
	overall := "ok"
	for _, check := range checks {
		if check.Status == "error" {
			overall = "error"
			break
		}
		if check.Status == "warning" {
			overall = "warning"
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"status": overall, "checked_at": time.Now().UTC(), "checks": checks,
		"auth_cache_source_id": s.hub.SourceID(), "auth_cache_revision": s.hub.Revision(),
		"trusted_proxy_count": len(s.trustedProxies),
	})
}

func secretDiagnostic(name, path string) diagnosticCheck {
	info, err := os.Stat(path)
	if err != nil {
		return diagnosticCheck{Name: name, Status: "error", Message: "file is unavailable"}
	}
	if info.Mode().Perm()&0o077 != 0 {
		// Imported secrets can retain the source filesystem mode. Tighten it in
		// place and verify the effective mode instead of reporting a stale warning.
		if err = os.Chmod(path, 0o600); err == nil {
			if info, err = os.Stat(path); err == nil && info.Mode().Perm()&0o077 == 0 {
				return diagnosticCheck{Name: name, Status: "ok", Message: "file permissions were restricted to 0600"}
			}
		}
		// A secret imported from an older RouterOS container can be readable by
		// the current UID while still being owned by the old container UID. In
		// that case chmod returns EPERM. Rewrite the exact same bytes through a
		// private temporary file and atomically replace the stale inode.
		if repairPrivateFile(path) {
			return diagnosticCheck{Name: name, Status: "ok", Message: "file ownership and permissions were repaired atomically"}
		}
		// RouterOS container volumes and some FAT-backed stores do not expose
		// POSIX mode bits: even a newly-created 0600 file is reported as 0666.
		// Verify that behavior in the same directory before suppressing the
		// warning. A normal Linux filesystem with an actually broad secret still
		// takes the warning path above/below.
		if !filesystemSupportsPrivateMode(filepath.Dir(path)) {
			return diagnosticCheck{Name: name, Status: "ok", Message: "storage does not expose POSIX mode bits; access is isolated by the container runtime"}
		}
		return diagnosticCheck{Name: name, Status: "warning", Message: fmt.Sprintf("file permissions are %04o; unable to restrict them to 0600", info.Mode().Perm())}
	}
	return diagnosticCheck{Name: name, Status: "ok", Message: "file is present with restricted permissions"}
}

func repairPrivateFile(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".secret-repair-*")
	if err != nil {
		return false
	}
	temporaryPath := temporary.Name()
	repaired := false
	defer func() {
		_ = temporary.Close()
		if !repaired {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err = temporary.Chmod(0o600); err != nil {
		return false
	}
	if _, err = temporary.Write(content); err != nil {
		return false
	}
	if err = temporary.Sync(); err != nil {
		return false
	}
	if err = temporary.Close(); err != nil {
		return false
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return false
	}
	info, err = os.Stat(path)
	repaired = err == nil && info.Mode().Perm()&0o077 == 0
	return repaired
}

func filesystemSupportsPrivateMode(directory string) bool {
	probe, err := os.CreateTemp(directory, ".permission-probe-*")
	if err != nil {
		return true
	}
	path := probe.Name()
	defer os.Remove(path)
	if err = probe.Close(); err != nil {
		return true
	}
	if err = os.Chmod(path, 0o600); err != nil {
		return true
	}
	info, err := os.Stat(path)
	return err != nil || info.Mode().Perm()&0o077 == 0
}

func serviceDiagnosticStatus(status string) string {
	if status == "online" {
		return "ok"
	}
	return "error"
}

func (s *Server) metrics(response http.ResponseWriter, request *http.Request) {
	if !s.validMetricsToken(request) {
		writeError(response, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	users, sessions, err := s.repository.ListAuthState(ctx, time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "metrics unavailable")
		return
	}
	devices, err := s.repository.ListDevices(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "metrics unavailable")
		return
	}
	relays, err := s.repository.ListRelayServers(ctx)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "metrics unavailable")
		return
	}
	onlineDevices, healthyRelays, relayConnections, relayBandwidth := 0, 0, 0, int64(0)
	for _, device := range devices {
		if device.Online {
			onlineDevices++
		}
	}
	for _, relay := range relays {
		if relay.Enabled && relay.Health == "healthy" {
			healthyRelays++
		}
		relayConnections += relay.Connections
		relayBandwidth += relay.Bandwidth
	}
	_, hbbsInstances, rendezvousPeers := s.runtime.service("hbbs", 20*time.Second)
	_, hbbrInstances, _ := s.runtime.service("hbbr", 20*time.Second)
	cpuPercent, memoryBytes, memoryCgroupBytes, memoryLimit, uptimeSeconds := s.runtime.system()
	response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	metrics := []struct {
		name  string
		help  string
		value any
	}{
		{"rustdesk_users", "Registered users", len(users)},
		{"rustdesk_sessions_active", "Active server-side sessions", len(sessions)},
		{"rustdesk_devices", "Managed devices", len(devices)},
		{"rustdesk_devices_online", "Online managed devices", onlineDevices},
		{"rustdesk_hbbs_instances", "Live HBBS instances", hbbsInstances},
		{"rustdesk_hbbr_instances", "Live HBBR instances", hbbrInstances},
		{"rustdesk_rendezvous_peers", "Peers registered with HBBS", rendezvousPeers},
		{"rustdesk_relays_healthy", "Healthy enabled relay servers", healthyRelays},
		{"rustdesk_relay_connections", "Current relay connections", relayConnections},
		{"rustdesk_relay_bandwidth_bytes", "Observed relay bandwidth in bytes", relayBandwidth},
		{"rustdesk_process_cpu_percent", "All-in-one process CPU usage percent", cpuPercent},
		{"rustdesk_process_memory_bytes", "All-in-one process resident memory", memoryBytes},
		{"rustdesk_container_memory_bytes", "Container cgroup memory usage", memoryCgroupBytes},
		{"rustdesk_container_memory_limit_bytes", "Container cgroup memory limit", memoryLimit},
		{"rustdesk_process_uptime_seconds", "API process uptime", uptimeSeconds},
		{"rustdesk_auth_cache_revision", "Current authorization event revision", s.hub.Revision()},
	}
	for _, metric := range metrics {
		_, _ = fmt.Fprintf(response, "# HELP %s %s\n# TYPE %s gauge\n%s %v\n", metric.name, metric.help, metric.name, metric.name, metric.value)
	}
}

func (s *Server) validMetricsToken(request *http.Request) bool {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	candidate := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if candidate == header {
		candidate = strings.TrimSpace(request.Header.Get("X-RDS-Metrics-Token"))
	}
	return len(s.metricsToken) >= 32 && len(candidate) == len(s.metricsToken) &&
		subtle.ConstantTimeCompare([]byte(candidate), s.metricsToken) == 1
}
