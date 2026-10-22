package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeContext(t *testing.T, dir, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	metaDir := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `"}}}`
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveHostUsesCurrentContext(t *testing.T) {
	dir := t.TempDir()
	writeContext(t, dir, "colima", "unix:///Users/me/.colima/default/docker.sock")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"colima"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_CONFIG", dir)

	if got := ResolveHost(); got != "unix:///Users/me/.colima/default/docker.sock" {
		t.Errorf("host = %q", got)
	}
}

func TestResolveHostPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeContext(t, dir, "other", "tcp://other:2375")
	t.Setenv("DOCKER_CONFIG", dir)

	t.Setenv("DOCKER_HOST", "tcp://explicit:2375")
	t.Setenv("DOCKER_CONTEXT", "other")
	if got := ResolveHost(); got != "tcp://explicit:2375" {
		t.Errorf("DOCKER_HOST should win, got %q", got)
	}

	t.Setenv("DOCKER_HOST", "")
	if got := ResolveHost(); got != "tcp://other:2375" {
		t.Errorf("DOCKER_CONTEXT should be used, got %q", got)
	}

	t.Setenv("DOCKER_CONTEXT", "default")
	if got := ResolveHost(); got != "" {
		t.Errorf("default context should use SDK default, got %q", got)
	}
}
