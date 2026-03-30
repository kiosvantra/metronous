package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

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
	var versions []string
	for _, tag := range tags {
		parts := strings.Split(tag, "\t")
		if len(parts) < 2 {
			continue
		}
		ref := parts[1]
		if strings.HasPrefix(ref, "refs/tags/v") && !strings.Contains(ref, "^{}") {
			v := strings.TrimPrefix(ref, "refs/tags/v")
			// Filter out pre-release versions
			if !strings.Contains(v, "-") {
				versions = append(versions, v)
			}
		}
	}

	if len(versions) == 0 {
		return "", fmt.Errorf("no stable tags found")
	}

	// Sort by semantic version
	sort.Slice(versions, func(i, j int) bool {
		return sortVersion(versions[i], versions[j]) < 0
	})

	return "v" + versions[len(versions)-1], nil
}

// sortVersion compares two semantic version strings.
// Returns -1 if v1 < v2, 0 if equal, 1 if v1 > v2
func sortVersion(v1, v2 string) int {
	v1Parts := strings.Split(strings.TrimPrefix(v1, "v"), ".")
	v2Parts := strings.Split(strings.TrimPrefix(v2, "v"), ".")

	for i := 0; i < len(v1Parts) && i < len(v2Parts); i++ {
		n1 := 0
		n2 := 0
		fmt.Sscanf(v1Parts[i], "%d", &n1)
		fmt.Sscanf(v2Parts[i], "%d", &n2)
		if n1 < n2 {
			return -1
		}
		if n1 > n2 {
			return 1
		}
	}

	if len(v1Parts) < len(v2Parts) {
		return -1
	}
	if len(v1Parts) > len(v2Parts) {
		return 1
	}
	return 0
}

func getInstallPath() (string, error) {
	gobin, err := exec.Command("go", "env", "GOBIN").Output()
	path := ""
	if err != nil || strings.TrimSpace(string(gobin)) == "" {
		gopath, err := exec.Command("go", "env", "GOPATH").Output()
		if err != nil {
			return "", err
		}
		path = strings.TrimSpace(string(gopath))
	} else {
		path = strings.TrimSpace(string(gobin))
	}

	binName := "metronous"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	return filepath.Join(path, "bin", binName), nil
}

func downloadBinary(url, destPath string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed with status %d from %s", resp.StatusCode, url)
	}

	// Create temp file in same directory as destination for atomic rename
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), "metronous-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Download to temp file
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to download: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Make executable before rename
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Replace existing binary (atomic on same filesystem)
	if err := os.Rename(tmpPath, destPath); err != nil {
		// Rename failed (cross-volume), copy instead
		src, err := os.Open(tmpPath)
		if err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to open temp file: %w", err)
		}
		defer src.Close()

		// Remove old binary if exists
		os.Remove(destPath)

		dst, err := os.Create(destPath)
		if err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to create dest file: %w", err)
		}
		defer dst.Close()

		if _, err := io.Copy(dst, src); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to copy binary: %w", err)
		}

		if err := dst.Close(); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to flush binary: %w", err)
		}

		if err := os.Chmod(destPath, 0755); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to set permissions: %w", err)
		}

		os.Remove(tmpPath)
	}

	return nil
}
