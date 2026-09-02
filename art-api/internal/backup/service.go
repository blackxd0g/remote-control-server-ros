package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

const maximumRestoreSize = int64(512 << 20)

var artifactName = regexp.MustCompile(`^rustdesk-backup-[0-9]{8}-[0-9]{6}\.[0-9]{9}\.db$`)

type repository interface {
	Backup(context.Context, string) error
	InspectBackup(context.Context, string) (domain.BackupInspection, error)
}

type Service struct {
	repository repository
	directory  string
	interval   time.Duration
	retention  int
	now        func() time.Time
	mu         sync.Mutex
}

func New(repository repository, dataDir string, interval time.Duration, retention int) (*Service, error) {
	directory := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure backup directory: %w", err)
	}
	return &Service{repository: repository, directory: directory, interval: interval, retention: retention, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Create(ctx); err != nil {
				slog.Error("automatic SQLite backup failed", "error", err)
			}
		}
	}
}

func (s *Service) Create(ctx context.Context) (domain.BackupArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := "rustdesk-backup-" + s.now().Format("20060102-150405.000000000") + ".db"
	path := filepath.Join(s.directory, name)
	if err := s.repository.Backup(ctx, path); err != nil {
		return domain.BackupArtifact{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return domain.BackupArtifact{}, err
	}
	inspection, err := s.repository.InspectBackup(ctx, path)
	if err != nil {
		_ = os.Remove(path)
		return domain.BackupArtifact{}, fmt.Errorf("verify created backup: %w", err)
	}
	if err = s.pruneLocked(); err != nil {
		return domain.BackupArtifact{}, err
	}
	return artifact(name, inspection), nil
}

func (s *Service) List(ctx context.Context) ([]domain.BackupArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths, err := s.artifactPaths()
	if err != nil {
		return nil, err
	}
	result := make([]domain.BackupArtifact, 0, len(paths))
	for _, path := range paths {
		inspection, inspectErr := s.repository.InspectBackup(ctx, path)
		if inspectErr != nil {
			continue
		}
		result = append(result, artifact(filepath.Base(path), inspection))
	}
	return result, nil
}

func (s *Service) Path(name string) (string, error) {
	if !artifactName.MatchString(name) {
		return "", domain.ErrNotFound
	}
	path := filepath.Join(s.directory, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", domain.ErrNotFound
	}
	return path, nil
}

func (s *Service) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *Service) StageRestore(ctx context.Context, reader io.Reader) (domain.BackupInspection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	temporary, err := os.CreateTemp(s.directory, ".restore-*.db")
	if err != nil {
		return domain.BackupInspection{}, err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	_ = temporary.Chmod(0o600)
	written, copyErr := io.Copy(temporary, io.LimitReader(reader, maximumRestoreSize+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil || written == 0 || written > maximumRestoreSize {
		return domain.BackupInspection{}, errors.New("invalid restore upload")
	}
	inspection, err := s.repository.InspectBackup(ctx, temporaryName)
	if err != nil {
		return domain.BackupInspection{}, err
	}
	file, err := os.Open(temporaryName)
	if err != nil {
		return domain.BackupInspection{}, err
	}
	hasher := sha256.New()
	_, hashErr := io.Copy(hasher, file)
	closeErr = file.Close()
	if hashErr != nil || closeErr != nil {
		return domain.BackupInspection{}, errors.New("hash restore upload")
	}
	sum := hasher.Sum(nil)
	candidate := filepath.Join(s.directory, "restore.pending.db")
	if err = replaceFile(temporaryName, candidate); err != nil {
		return domain.BackupInspection{}, err
	}
	marker := filepath.Join(s.directory, "restore.pending.sha256")
	if err = os.WriteFile(marker+".tmp", []byte(hex.EncodeToString(sum)+"\n"), 0o600); err != nil {
		return domain.BackupInspection{}, err
	}
	if err = replaceFile(marker+".tmp", marker); err != nil {
		return domain.BackupInspection{}, err
	}
	return inspection, nil
}

func (s *Service) RestorePending() bool {
	_, err := os.Stat(filepath.Join(s.directory, "restore.pending.sha256"))
	return err == nil
}
func (s *Service) Policy() (time.Duration, int) { return s.interval, s.retention }
func (s *Service) CancelRestore() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(filepath.Join(s.directory, "restore.pending.db"))
	err := os.Remove(filepath.Join(s.directory, "restore.pending.sha256"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Service) artifactPaths() ([]string, error) {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if artifactName.MatchString(entry.Name()) && entry.Type().IsRegular() {
			paths = append(paths, filepath.Join(s.directory, entry.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, nil
}
func (s *Service) pruneLocked() error {
	paths, err := s.artifactPaths()
	if err != nil {
		return err
	}
	for _, path := range paths[safeMin(s.retention, len(paths)):] {
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
func artifact(name string, value domain.BackupInspection) domain.BackupArtifact {
	return domain.BackupArtifact{Name: name, SizeBytes: value.SizeBytes, CreatedAt: value.ModifiedAt, Users: value.Users, Devices: value.Devices, Sessions: value.Sessions, QuickCheck: value.QuickCheck}
}
func replaceFile(source, target string) error {
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, target)
}
func safeMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ValidDatabasePath(value string) bool {
	return filepath.IsAbs(value) && !strings.ContainsAny(value, "?\x00") && !strings.HasPrefix(value, "file:")
}
