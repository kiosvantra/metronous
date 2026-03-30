package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// NewSelfUpdateCommand creates the `metronous self-update` cobra command.
func NewSelfUpdateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update Metronous to the latest version",
		Long: `Downloads and installs the latest version of Metronous from GitHub releases.

Downloads the pre-built binary for your OS/architecture and replaces
the current installation.`,
		RunE: runSelfUpdate,
	}

	return cmd
}

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	fmt.Println("Checking for updates...")

	// Check if go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("Go is not installed or not in PATH")
	}

	// Determine download URL based on OS and arch
	version, err := getLatestVersion()
	if err != nil {
		return fmt.Errorf("failed to get latest version: %w", err)
	}

	filename := fmt.Sprintf("metronous-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}

	downloadURL := fmt.Sprintf("https://github.com/kiosvantra/metronous/releases/download/%s/%s", version, filename)
	installPath, err := getInstallPath()
	if err != nil {
		return fmt.Errorf("failed to determine install path: %w", err)
	}

	if err := downloadBinary(downloadURL, installPath); err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}

	// Make executable
	if err := os.Chmod(installPath, 0755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	fmt.Printf("\nMetronous has been updated to %s.\n", version)
	return nil
}

func getLatestVersion() (string, error) {
	// Use git ls-remote to get the latest tag
	cmd := exec.Command("git", "ls-remote", "--tags", "https://github.com/kiosvantra/metronous")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	tags := strings.Split(string(out), "\n")
	var latest string
	for _, tag := range tags {
		parts := strings.Split(tag, "\t")
		if len(parts) < 2 {
			continue
		}
		ref := parts[1]
		if strings.HasPrefix(ref, "refs/tags/v") && !strings.Contains(ref, "^{}") {
			v := strings.TrimPrefix(ref, "refs/tags/v")
			if latest == "" || v > latest {
				latest = v
			}
		}
	}

	if latest == "" {
		return "", fmt.Errorf("no tags found")
	}
	return "v" + latest, nil
}

func getInstallPath() (string, error) {
	gobin, err := exec.Command("go", "env", "GOBIN").Output()
	if err != nil || strings.TrimSpace(string(gobin)) == "" {
		gopath, err := exec.Command("go", "env", "GOPATH").Output()
		if err != nil {
			return "", err
		}
		return filepath.Join(strings.TrimSpace(string(gopath)), "bin", "metronous"), nil
	}
	return filepath.Join(strings.TrimSpace(string(gobin)), "metronous"), nil
}

func downloadBinary(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "metronous-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Download to temp file
	out, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Replace existing binary
	if err := os.Rename(tmpPath, destPath); err != nil {
		// Cross-volume rename failed, copy instead
		if src, err := os.Open(tmpPath); err != nil {
			return err
		} else {
			defer src.Close()
			dst, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer dst.Close()
			io.Copy(dst, src)
		}
	}

	return nil
}
