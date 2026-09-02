package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type builderWorkerContextKey struct{}
type builderWorkerTokenRepository interface {
	FindBuilderWorkerByTokenHash(context.Context, string) (domain.BuilderWorker, error)
}

func builderWorkerID(ctx context.Context) string {
	value, _ := ctx.Value(builderWorkerContextKey{}).(string)
	return value
}
func builderTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func findBuilderWorkerByToken(ctx context.Context, repository any, token string) (domain.BuilderWorker, error) {
	if len(token) < 32 {
		return domain.BuilderWorker{}, domain.ErrNotFound
	}
	values, ok := repository.(builderWorkerTokenRepository)
	if !ok {
		return domain.BuilderWorker{}, domain.ErrNotFound
	}
	return values.FindBuilderWorkerByTokenHash(ctx, builderTokenHash(token))
}
func issueBuilderToken() (string, error) {
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "rds_builder_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

type builderClaimRequest struct {
	WorkerID      string   `json:"worker_id"`
	Formats       []string `json:"formats"`
	Platforms     []string `json:"platforms"`
	Architectures []string `json:"architectures"`
	LeaseSeconds  int      `json:"lease_seconds"`
}

type builderWorkerRequest struct {
	ID            string   `json:"worker_id"`
	Name          string   `json:"name"`
	Hostname      string   `json:"hostname"`
	Version       string   `json:"version"`
	Formats       []string `json:"formats"`
	Platforms     []string `json:"platforms"`
	Architectures []string `json:"architectures"`
	Concurrency   int      `json:"concurrency"`
}

func (s *Server) registerBuilderWorker(response http.ResponseWriter, request *http.Request) {
	var input builderWorkerRequest
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid builder worker")
		return
	}
	input.ID, input.Name, input.Hostname, input.Version = strings.TrimSpace(input.ID), strings.TrimSpace(input.Name), strings.TrimSpace(input.Hostname), strings.TrimSpace(input.Version)
	registration := strings.HasSuffix(request.URL.Path, "/register")
	if !registration && input.ID != builderWorkerID(request.Context()) {
		writeError(response, http.StatusForbidden, "worker identity mismatch")
		return
	}
	if input.ID == "" || len(input.ID) > 128 || input.Name == "" || len(input.Name) > 128 || len(input.Hostname) > 253 || len(input.Version) > 64 || input.Concurrency < 1 || input.Concurrency > 32 || len(input.Formats) == 0 || len(input.Formats) > 16 || len(input.Platforms) > 16 || len(input.Architectures) > 16 {
		writeError(response, http.StatusBadRequest, "invalid builder worker")
		return
	}
	if !validBuilderCapabilities(input.Formats, map[string]bool{"configuration": true, "portable": true, "installer": true, "apk": true}) || !validBuilderCapabilities(input.Platforms, map[string]bool{"windows": true, "linux": true, "android": true, "macos": true}) || !validBuilderCapabilities(input.Architectures, map[string]bool{"amd64": true, "arm64": true}) {
		writeError(response, http.StatusBadRequest, "invalid builder capabilities")
		return
	}
	now := time.Now().UTC()
	value := domain.BuilderWorker{ID: input.ID, Name: input.Name, Hostname: input.Hostname, Version: input.Version, Formats: input.Formats, Platforms: input.Platforms, Architectures: input.Architectures, Concurrency: input.Concurrency, Status: "online", LastSeenAt: now, CreatedAt: now, UpdatedAt: now}
	workerToken := ""
	if registration {
		var err error
		workerToken, err = issueBuilderToken()
		if err != nil {
			writeError(response, http.StatusInternalServerError, "builder credential generation failed")
			return
		}
		value.TokenHash = builderTokenHash(workerToken)
	}
	if err := s.repository.UpsertBuilderWorker(request.Context(), value); err != nil {
		writeError(response, http.StatusInternalServerError, "builder worker update failed")
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "builder_worker_heartbeat", ControllerDevice: value.ID, Result: "success", Metadata: map[string]any{"name": value.Name, "version": value.Version, "formats": value.Formats}})
	if registration {
		writeJSON(response, http.StatusOK, map[string]any{"worker": value, "worker_token": workerToken, "token_type": "Bearer"})
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func validBuilderCapabilities(values []string, allowed map[string]bool) bool {
	for _, value := range values {
		if !allowed[strings.ToLower(strings.TrimSpace(value))] {
			return false
		}
	}
	return true
}

func (s *Server) listBuilderWorkers(response http.ResponseWriter, request *http.Request) {
	values, err := s.repository.ListBuilderWorkers(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "builder workers unavailable")
		return
	}
	now := time.Now().UTC()
	for index := range values {
		if now.Sub(values[index].LastSeenAt) > 30*time.Second {
			values[index].Status = "offline"
		}
	}
	writeJSON(response, http.StatusOK, values)
}

func (s *Server) claimClientBuild(response http.ResponseWriter, request *http.Request) {
	if s.managedClients == nil {
		writeError(response, http.StatusServiceUnavailable, "managed clients unavailable")
		return
	}
	var input builderClaimRequest
	if decodeJSON(request, &input, 16<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid builder claim")
		return
	}
	authenticatedWorker := builderWorkerID(request.Context())
	if input.WorkerID != "" && input.WorkerID != authenticatedWorker {
		writeError(response, http.StatusForbidden, "worker identity mismatch")
		return
	}
	input.WorkerID = authenticatedWorker
	value, err := s.managedClients.ClaimBuild(request.Context(), input.WorkerID, input.Formats, input.Platforms, input.Architectures, time.Duration(input.LeaseSeconds)*time.Second)
	if errors.Is(err, domain.ErrNotFound) {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "client_build_claimed", ControllerDevice: input.WorkerID, Result: "leased", Metadata: map[string]any{"build_id": value.ID, "attempt": value.Attempts}})
	writeJSON(response, http.StatusOK, value)
}

func (s *Server) heartbeatClientBuild(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkerID     string `json:"worker_id"`
		LeaseSeconds int    `json:"lease_seconds"`
	}
	if decodeJSON(request, &input, 8<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid build heartbeat")
		return
	}
	authenticatedWorker := builderWorkerID(request.Context())
	if input.WorkerID != "" && input.WorkerID != authenticatedWorker {
		writeError(response, http.StatusForbidden, "worker identity mismatch")
		return
	}
	input.WorkerID = authenticatedWorker
	value, err := s.managedClients.RenewBuild(request.Context(), request.PathValue("buildID"), input.WorkerID, time.Duration(input.LeaseSeconds)*time.Second)
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (s *Server) clientBuildPayload(response http.ResponseWriter, request *http.Request) {
	value, err := s.repository.FindClientBuildByID(request.Context(), request.PathValue("buildID"))
	if err != nil {
		writeError(response, http.StatusNotFound, "client build not found")
		return
	}
	workerID := builderWorkerID(request.Context())
	if value.WorkerID != workerID || value.LeaseUntil == nil || !value.LeaseUntil.After(time.Now().UTC()) {
		writeError(response, http.StatusConflict, "build lease is not active")
		return
	}
	profile, err := s.repository.FindClientProfileByID(request.Context(), value.ProfileID)
	if err != nil {
		writeError(response, http.StatusNotFound, "client profile not found")
		return
	}
	bundle, err := s.managedClients.Bundle(profile, value.CreatedAt)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "build payload unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"job": value, "bundle": bundle})
}

func (s *Server) completeClientBuild(response http.ResponseWriter, request *http.Request) {
	workerID := builderWorkerID(request.Context())
	name := request.Header.Get("X-RDS-Artifact-Name")
	digest := request.Header.Get("X-Content-SHA256")
	length, _ := strconv.ParseInt(request.Header.Get("Content-Length"), 10, 64)
	if workerID == "" || length > 16<<20 {
		writeError(response, http.StatusBadRequest, "invalid client artifact")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(response, request.Body, 16<<20))
	if err != nil {
		writeError(response, http.StatusRequestEntityTooLarge, "client artifact is too large")
		return
	}
	value, err := s.managedClients.CompleteBuild(request.Context(), request.PathValue("buildID"), workerID, name, request.Header.Get("Content-Type"), digest, data)
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "client_build_completed", ControllerDevice: workerID, Result: "ready", Metadata: map[string]any{"build_id": value.ID, "sha256": value.SHA256, "artifact_name": value.ArtifactName}})
	writeJSON(response, http.StatusOK, value)
}

func (s *Server) failClientBuild(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkerID string `json:"worker_id"`
		Error    string `json:"error"`
	}
	if decodeJSON(request, &input, 8<<10) != nil {
		writeError(response, http.StatusBadRequest, "invalid build failure")
		return
	}
	authenticatedWorker := builderWorkerID(request.Context())
	if input.WorkerID != "" && input.WorkerID != authenticatedWorker {
		writeError(response, http.StatusForbidden, "worker identity mismatch")
		return
	}
	input.WorkerID = authenticatedWorker
	value, err := s.managedClients.FailBuild(request.Context(), request.PathValue("buildID"), input.WorkerID, input.Error)
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	_ = s.audit.Record(request.Context(), domain.AuditEvent{Type: "client_build_failed", ControllerDevice: input.WorkerID, Result: "failed", Reason: value.Error, Metadata: map[string]any{"build_id": value.ID, "attempt": value.Attempts}})
	writeJSON(response, http.StatusOK, value)
}
