package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPermissionDeniedUpdateError(t *testing.T) {
	err := permissionDeniedUpdateError("/usr/local/bin/metronous", os.ErrPermission)
	msg := err.Error()

	checks := []string{
		"/usr/local/bin/metronous",
		"write permission",
		"Re-run with sudo",
		"~/.local/bin",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected %q in error message, got: %s", want, msg)
		}
	}
}

func TestIsPermissionError(t *testing.T) {
	if !isPermissionError(os.ErrPermission) {
		t.Fatal("expected os.ErrPermission to be treated as permission error")
	}

	if !isPermissionError(errors.New("rename failed: permission denied")) {
		t.Fatal("expected permission denied text to be treated as permission error")
	}

	if isPermissionError(errors.New("some other failure")) {
		t.Fatal("did not expect unrelated error to be treated as permission error")
	}
}
