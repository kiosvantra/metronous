//go:build linux

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractUnitDataDir(t *testing.T) {
	dir := t.TempDir()
	unitPath := filepath.Join(dir, "metronous.service")
	content := "ExecStart='/home/user/.local/bin/metronous' server --data-dir '/home/user/.metronous/data' --daemon-mode\n"
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := extractUnitDataDir(unitPath)
	if err != nil {
		t.Fatalf("extractUnitDataDir: %v", err)
	}
	if got != "/home/user/.metronous/data" {
		t.Fatalf("expected parsed data dir, got %q", got)
	}
}
