package cli

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestLoadListenAddressDefaultsToLoopbackWithoutLANOptIn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	data := []byte(`version: "1"
server:
  listen_address: "0.0.0.0:8080"
  enable_timeline_lan: false
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadListenAddress(filepath.Join(dir, "data"), zap.NewNop())
	if got != "127.0.0.1:0" {
		t.Fatalf("got %q want loopback default", got)
	}
}

func TestLoadListenAddressHonorsLANOptIn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	data := []byte(`version: "1"
server:
  listen_address: "0.0.0.0:8080"
  enable_timeline_lan: true
`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadListenAddress(filepath.Join(dir, "data"), zap.NewNop())
	if got != "0.0.0.0:8080" {
		t.Fatalf("got %q want 0.0.0.0:8080", got)
	}
}
