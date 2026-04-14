package cli

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestManagedBinaryPathPrefersLocalBinOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only expectation")
	}
	current := "/usr/local/bin/metronous"
	got := managedBinaryPath(current)
	if filepath.Base(got) != "metronous" {
		t.Fatalf("expected metronous binary name, got %q", got)
	}
	if filepath.Base(filepath.Dir(got)) != "bin" || filepath.Base(filepath.Dir(filepath.Dir(got))) != ".local" {
		t.Fatalf("expected managed path under ~/.local/bin, got %q", got)
	}
}
