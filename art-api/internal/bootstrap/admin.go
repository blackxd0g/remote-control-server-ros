package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/google/uuid"
)

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%_-"

type Result struct {
	Created         bool
	CredentialsFile string
}

func EnsureAdmin(ctx context.Context, repository domain.Repository, username, configuredPassword, credentialsFile string) (Result, error) {
	count, err := repository.CountUsers(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("count users: %w", err)
	}
	if count != 0 {
		return Result{}, nil
	}
	username = strings.TrimSpace(strings.ToLower(username))
	if len(username) < 2 || len(username) > 128 {
		return Result{}, errors.New("bootstrap username must contain 2 to 128 characters")
	}
	password := configuredPassword
	generated := false
	if password == "" {
		password, err = readCredentialsPassword(credentialsFile, username)
		if errors.Is(err, os.ErrNotExist) {
			password, err = generatePassword(32)
			generated = true
		}
		if err != nil {
			return Result{}, err
		}
	}
	if len(password) < 1 || len(password) > 1024 {
		return Result{}, errors.New("bootstrap password must contain between 1 and 1024 bytes")
	}
	if generated {
		if err := persistCredentials(credentialsFile, username, password); err != nil {
			return Result{}, err
		}
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return Result{}, fmt.Errorf("hash bootstrap password: %w", err)
	}
	now := time.Now().UTC()
	user := domain.User{ID: uuid.NewString(), Username: username, DisplayName: "Administrator",
		PasswordHash: passwordHash, Role: domain.RoleAdmin, Enabled: true, TokenVersion: 1,
		CreatedAt: now, UpdatedAt: now}
	if err := repository.CreateUser(ctx, user); err != nil {
		return Result{}, fmt.Errorf("create bootstrap administrator: %w", err)
	}
	if generated {
		return Result{Created: true, CredentialsFile: credentialsFile}, nil
	}
	return Result{Created: true}, nil
}

func generatePassword(length int) (string, error) {
	result := make([]byte, length)
	maximum := big.NewInt(int64(len(passwordAlphabet)))
	for index := range result {
		value, err := rand.Int(rand.Reader, maximum)
		if err != nil {
			return "", fmt.Errorf("generate bootstrap password: %w", err)
		}
		result[index] = passwordAlphabet[value.Int64()]
	}
	return string(result), nil
}

func readCredentialsPassword(path, username string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	values := make(map[string]string, len(lines))
	for _, line := range lines {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			values[key] = value
		}
	}
	if values["username"] != username || len(values["password"]) < 1 || len(values["password"]) > 1024 {
		return "", errors.New("invalid bootstrap credentials file")
	}
	return values["password"], nil
}

func persistCredentials(path, username, password string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create bootstrap secret directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create bootstrap credentials: %w", err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "username=%s\npassword=%s\n", username, password); err != nil {
		return fmt.Errorf("write bootstrap credentials: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod bootstrap credentials: %w", err)
	}
	return file.Sync()
}
