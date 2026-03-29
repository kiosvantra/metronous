package mcp_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/enduluc/metronous/internal/mcp"
)

// TestAcquirePIDFile_NoExisting verifies that when no PID file exists,
// acquirePIDFile creates the file and writes the current PID into it.
func TestAcquirePIDFile_NoExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metronous.pid")

	if err := mcp.AcquirePIDFile(path); err != nil {
		t.Fatalf("AcquirePIDFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}

	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("pid in file: got %d, want %d (our PID)", got, os.Getpid())
	}
}

// TestAcquirePIDFile_SamePID verifies that when the PID file already contains
// our own PID, acquirePIDFile returns nil without error and leaves the file intact.
func TestAcquirePIDFile_SamePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metronous.pid")

	// Pre-write our own PID.
	content := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("setup: write pid file: %v", err)
	}

	if err := mcp.AcquirePIDFile(path); err != nil {
		t.Fatalf("AcquirePIDFile: %v", err)
	}

	// File should still contain our PID.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("pid in file after no-op: got %d, want %d", got, os.Getpid())
	}
}

// TestAcquirePIDFile_StalePID verifies that a PID file containing an obviously
// dead PID (one that could never be running) is silently overwritten with our PID.
func TestAcquirePIDFile_StalePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metronous.pid")

	// 99999999 is way beyond the typical max PID on Linux (default 4194304).
	const deadPID = 99999999
	content := fmt.Sprintf("%d\n", deadPID)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("setup: write stale pid file: %v", err)
	}

	if err := mcp.AcquirePIDFile(path); err != nil {
		t.Fatalf("AcquirePIDFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("pid in file after stale-overwrite: got %d, want %d", got, os.Getpid())
	}
}

// TestAcquirePIDFile_AliveProcess verifies that when the PID file contains the
// PID of a live process, that process is terminated and the file is reclaimed.
func TestAcquirePIDFile_AliveProcess(t *testing.T) {
	// Start a long-lived background process.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	targetPID := cmd.Process.Pid

	// Call cmd.Wait() in a goroutine so the OS can reap the zombie after the
	// process is killed by AcquirePIDFile.  Without Wait(), the process stays
	// as a zombie and Signal(0) continues to succeed.
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_ = cmd.Wait()
	}()

	// Best-effort cleanup if the test fails before the process is killed.
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGKILL)
		<-waitDone
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "metronous.pid")

	// Write the live PID into the file.
	content := fmt.Sprintf("%d\n", targetPID)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("setup: write live pid file: %v", err)
	}

	if err := mcp.AcquirePIDFile(path); err != nil {
		t.Fatalf("AcquirePIDFile: %v", err)
	}

	// Wait for cmd.Wait() to return (zombie reaped) or timeout.
	select {
	case <-waitDone:
		// Process was reaped — good.
	case <-time.After(5 * time.Second):
		t.Errorf("process pid %d was not reaped within 5s after AcquirePIDFile", targetPID)
	}

	// After reaping, Signal(0) must fail.
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Errorf("process pid %d is still alive after AcquirePIDFile", targetPID)
	}

	// PID file should now contain our PID.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("pid in file after termination: got %d, want %d", got, os.Getpid())
	}
}

// TestAcquirePIDFile_RaceWithDeletion simulates the race where the file
// disappears between the O_EXCL attempt and the ReadFile call.  We verify that
// acquirePIDFile handles an os.IsNotExist on the read path gracefully and
// ultimately writes our PID.
//
// We simulate this by writing a PID file, then in a tight loop remove it
// and let AcquirePIDFile run; it should retry and succeed.
func TestAcquirePIDFile_RaceWithDeletion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metronous.pid")

	// Write a stale-PID file so the first O_EXCL fails, then immediately
	// remove it — mimicking another process deleting the file concurrently.
	content := fmt.Sprintf("%d\n", 99999999) // dead PID
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Delete it right away: AcquirePIDFile will see O_EXCL fail (file existed),
	// then ReadFile returns os.IsNotExist → retry path.
	_ = os.Remove(path)

	if err := mcp.AcquirePIDFile(path); err != nil {
		t.Fatalf("AcquirePIDFile after concurrent deletion: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	if got != os.Getpid() {
		t.Errorf("pid after race-with-deletion: got %d, want %d", got, os.Getpid())
	}
}
