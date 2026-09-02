package httpapi

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSecretDiagnosticRestrictsImportedSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := secretDiagnostic("test_secret", path)
	if check.Status != "ok" {
		t.Fatalf("expected repaired permissions, got %#v", check)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected mode 0600, got %04o", mode)
	}
}

func TestFilesystemSupportsPrivateMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	if !filesystemSupportsPrivateMode(t.TempDir()) {
		t.Fatal("expected the test filesystem to preserve private modes")
	}
}

func TestRepairPrivateFilePreservesContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	path := filepath.Join(t.TempDir(), "imported-secret")
	want := []byte("private-key-material")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if !repairPrivateFile(path) {
		t.Fatal("expected atomic repair to succeed")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("secret changed during repair: got %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected mode 0600, got %04o", mode)
	}
}
