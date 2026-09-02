package managedclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/google/uuid"
)

type BuildRequest struct{ ProfileID, TargetOS, Architecture, Format, CreatedBy string }
type Artifact struct {
	Name, MediaType string
	Data            []byte
}
type Builder interface {
	Format() string
	Build(context.Context, domain.ClientProfile, BuildRequest, time.Time) (Artifact, error)
}

type ManifestBuilder struct{ service *Service }

func (builder ManifestBuilder) Format() string { return "configuration" }
func (builder ManifestBuilder) Build(_ context.Context, profile domain.ClientProfile, request BuildRequest, now time.Time) (Artifact, error) {
	bundle, err := builder.service.Bundle(profile, now)
	if err != nil {
		return Artifact{}, err
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return Artifact{}, err
	}
	name := safeName(profile.Name)
	return Artifact{Name: fmt.Sprintf("rustdesk-%s-%s-%s.profile.json", name, request.TargetOS, request.Architecture), MediaType: "application/json", Data: data}, nil
}

func (s *Service) CreateBuild(ctx context.Context, request BuildRequest) (domain.ClientBuild, error) {
	request.ProfileID, request.TargetOS, request.Architecture, request.Format = strings.TrimSpace(request.ProfileID), strings.ToLower(strings.TrimSpace(request.TargetOS)), strings.ToLower(strings.TrimSpace(request.Architecture)), strings.ToLower(strings.TrimSpace(request.Format))
	if !map[string]bool{"windows": true, "linux": true, "macos": true, "android": true}[request.TargetOS] || !map[string]bool{"amd64": true, "arm64": true}[request.Architecture] || !map[string]bool{"configuration": true, "portable": true, "installer": true, "apk": true}[request.Format] {
		return domain.ClientBuild{}, errors.New("unsupported build target")
	}
	if request.Format == "apk" && request.TargetOS != "android" {
		return domain.ClientBuild{}, errors.New("APK requires Android target")
	}
	profile, err := s.repository.FindClientProfileByID(ctx, request.ProfileID)
	if err != nil {
		return domain.ClientBuild{}, err
	}
	now := time.Now().UTC()
	status := "queued"
	if request.Format == "configuration" {
		status = "building"
	}
	value := domain.ClientBuild{ID: uuid.NewString(), ProfileID: profile.ID, TargetOS: request.TargetOS, Architecture: request.Architecture, Format: request.Format, Status: status, CreatedBy: request.CreatedBy, CreatedAt: now}
	if err = s.repository.CreateClientBuild(ctx, value); err != nil {
		return domain.ClientBuild{}, err
	}
	if request.Format != "configuration" {
		return value, nil
	}
	artifact, buildErr := (ManifestBuilder{service: s}).Build(ctx, profile, request, now)
	completed := time.Now().UTC()
	value.CompletedAt = &completed
	if buildErr != nil {
		value.Status = "failed"
		value.Error = truncateBuildError(buildErr.Error())
	} else {
		sum := sha256.Sum256(artifact.Data)
		value.Status = "ready"
		value.ArtifactName = artifact.Name
		value.MediaType = artifact.MediaType
		value.SHA256 = hex.EncodeToString(sum[:])
		value.Artifact = string(artifact.Data)
	}
	if err = s.repository.UpdateClientBuild(ctx, value); err != nil {
		return domain.ClientBuild{}, err
	}
	return value, nil
}

func (s *Service) ClaimBuild(ctx context.Context, workerID string, formats, platforms, architectures []string, lease time.Duration) (domain.ClientBuild, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 128 || len(formats) == 0 || len(formats) > 16 || len(platforms) == 0 || len(platforms) > 16 || len(architectures) == 0 || len(architectures) > 16 {
		return domain.ClientBuild{}, errors.New("invalid builder claim")
	}
	cleanFormats, err := cleanCapabilities(formats, map[string]bool{"portable": true, "installer": true, "apk": true})
	if err != nil {
		return domain.ClientBuild{}, errors.New("unsupported builder format")
	}
	cleanPlatforms, err := cleanCapabilities(platforms, map[string]bool{"windows": true, "linux": true, "macos": true, "android": true})
	if err != nil {
		return domain.ClientBuild{}, errors.New("unsupported builder platform")
	}
	cleanArchitectures, err := cleanCapabilities(architectures, map[string]bool{"amd64": true, "arm64": true})
	if err != nil {
		return domain.ClientBuild{}, errors.New("unsupported builder architecture")
	}
	lease = max(time.Minute, min(lease, 15*time.Minute))
	now := time.Now().UTC()
	return s.repository.ClaimClientBuild(ctx, workerID, cleanFormats, cleanPlatforms, cleanArchitectures, now, now.Add(lease))
}

func cleanCapabilities(values []string, allowed map[string]bool) ([]string, error) {
	clean := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowed[value] {
			return nil, errors.New("unsupported capability")
		}
		if !seen[value] {
			seen[value] = true
			clean = append(clean, value)
		}
	}
	return clean, nil
}

func (s *Service) RenewBuild(ctx context.Context, buildID, workerID string, lease time.Duration) (domain.ClientBuild, error) {
	value, err := s.ownedActiveBuild(ctx, buildID, workerID)
	if err != nil {
		return domain.ClientBuild{}, err
	}
	lease = max(time.Minute, min(lease, 15*time.Minute))
	until := time.Now().UTC().Add(lease)
	value.LeaseUntil = &until
	value.Status = "building"
	err = s.repository.UpdateClaimedClientBuild(ctx, value, workerID, time.Now().UTC())
	return value, err
}

func (s *Service) CompleteBuild(ctx context.Context, buildID, workerID, name, mediaType, expectedSHA string, artifact []byte) (domain.ClientBuild, error) {
	value, err := s.ownedActiveBuild(ctx, buildID, workerID)
	if err != nil {
		return domain.ClientBuild{}, err
	}
	name = safeArtifactName(name)
	if name == "" || len(artifact) == 0 || len(artifact) > 16<<20 || len(mediaType) > 128 {
		return domain.ClientBuild{}, errors.New("invalid client artifact")
	}
	sum := sha256.Sum256(artifact)
	digest := hex.EncodeToString(sum[:])
	if expectedSHA != "" && !strings.EqualFold(strings.TrimSpace(expectedSHA), digest) {
		return domain.ClientBuild{}, errors.New("artifact digest mismatch")
	}
	now := time.Now().UTC()
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	value.Status, value.ArtifactName, value.SHA256, value.Artifact = "ready", name, digest, "base64:"+base64.StdEncoding.EncodeToString(artifact)
	value.MediaType = mediaType
	value.Error, value.LeaseUntil, value.CompletedAt = "", nil, &now
	err = s.repository.UpdateClaimedClientBuild(ctx, value, workerID, time.Now().UTC())
	return value, err
}

func (s *Service) FailBuild(ctx context.Context, buildID, workerID, message string) (domain.ClientBuild, error) {
	value, err := s.ownedActiveBuild(ctx, buildID, workerID)
	if err != nil {
		return domain.ClientBuild{}, err
	}
	now := time.Now().UTC()
	value.Status, value.Error, value.LeaseUntil, value.CompletedAt = "failed", truncateBuildError(strings.TrimSpace(message)), nil, &now
	err = s.repository.UpdateClaimedClientBuild(ctx, value, workerID, time.Now().UTC())
	return value, err
}

func (s *Service) ownedActiveBuild(ctx context.Context, buildID, workerID string) (domain.ClientBuild, error) {
	value, err := s.repository.FindClientBuildByID(ctx, strings.TrimSpace(buildID))
	if err != nil {
		return domain.ClientBuild{}, err
	}
	if value.WorkerID != strings.TrimSpace(workerID) || !map[string]bool{"leased": true, "building": true, "uploading": true}[value.Status] || value.LeaseUntil == nil || !value.LeaseUntil.After(time.Now().UTC()) {
		return domain.ClientBuild{}, errors.New("build lease is not active")
	}
	return value, nil
}

func safeArtifactName(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.Contains(value, "/") || value == "." || value == ".." || len(value) > 192 {
		return ""
	}
	return value
}

var unsafeName = regexp.MustCompile(`[^a-z0-9]+`)

func safeName(value string) string {
	value = unsafeName.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "client"
	}
	if len(value) > 64 {
		return value[:64]
	}
	return value
}
func truncateBuildError(value string) string {
	if len(value) > 1024 {
		return value[:1024]
	}
	return value
}
