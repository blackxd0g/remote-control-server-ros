package secret

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const secretBytes = 64

func LoadOrCreate(path, environmentValue string) ([]byte, error) {
	if environmentValue != "" {
		return []byte(environmentValue), nil
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			return nil, fmt.Errorf("chmod secret: %w", chmodErr)
		}
		value := strings.TrimSpace(string(data))
		if len(value) < 32 {
			return nil, fmt.Errorf("secret in %s is too short", path)
		}
		return []byte(value), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create secret directory: %w", err)
	}
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(value+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write secret: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return nil, fmt.Errorf("chmod secret: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return nil, fmt.Errorf("persist secret: %w", err)
	}
	return []byte(value), nil
}
