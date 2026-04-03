package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kiosvantra/metronous/internal/version"
	"github.com/spf13/cobra"
)

// NewSelfUpdateCommand creates the `metronous self-update` cobra command.
func NewSelfUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Update Metronous to the latest version",
		Long: `Downloads and installs the latest version of Metronous from GitHub releases.

Downloads the pre-built binary for your OS/architecture and replaces
the current installation.`,
		RunE: runSelfUpdate,
	}
}

func runSelfUpdate(cmd *cobra.Command, args []string) error {
	fmt.Println("Checking for updates...")

	latestTag, err := fetchLatestTag()
	if err != nil {
		return fmt.Errorf("failed to get latest version: %w", err)
	}

	current := version.Version
	if latestTag == "v"+current || latestTag == current {
		fmt.Printf("Already up to date (%s).\n", current)
		return nil
	}

	fmt.Printf("Updating from %s to %s...\n", current, latestTag)

	// GoReleaser archive format: metronous_<version-no-v>_<os>_<arch>.tar.gz
	versionNoV := strings.TrimPrefix(latestTag, "v")
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	filename := fmt.Sprintf("metronous_%s_%s_%s.tar.gz", versionNoV, goos, goarch)
	downloadURL := fmt.Sprintf("https://github.com/kiosvantra/metronous/releases/download/%s/%s", latestTag, filename)

	installPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine current executable path: %w", err)
	}

	if err := downloadAndInstallBinary(downloadURL, installPath); err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}

	// Also update the plugin file if it exists at the known OpenCode plugin path.
	if err := updatePlugin(latestTag); err != nil {
		// Non-fatal: binary update succeeded, plugin update is best-effort.
		fmt.Printf("  Warning: could not update plugin: %v\n", err)
		fmt.Println("  You can update it manually from: https://raw.githubusercontent.com/kiosvantra/metronous/main/metronous-plugin.ts")
	}

	fmt.Printf("\nMetronous has been updated to %s.\n", latestTag)
	fmt.Println("  Close and reopen the dashboard to use the new version.")
	fmt.Println("  Restart the metronous service only if you also want the MCP daemon to run the updated binary.")
	return nil
}

// updatePlugin downloads the latest plugin file from GitHub and copies it to
// the OpenCode plugin directory (~/.config/opencode/plugins/metronous.ts).
// If the file does not exist at that path, this is a no-op — we only update
// what the user has already installed.
func updatePlugin(tag string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}

	pluginPath := filepath.Join(homeDir, ".config", "opencode", "plugins", "metronous.ts")

	// Only update if the plugin is already installed at this path.
	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		return nil
	}

	// Download from the tagged release on GitHub.
	pluginURL := fmt.Sprintf("https://raw.githubusercontent.com/kiosvantra/metronous/%s/metronous-plugin.ts", tag)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(pluginURL)
	if err != nil {
		return fmt.Errorf("download plugin: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download plugin: HTTP %d from %s", resp.StatusCode, pluginURL)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read plugin response: %w", err)
	}

	// Write atomically via temp file.
	tmpPath := pluginPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write plugin temp file: %w", err)
	}
	if err := os.Rename(tmpPath, pluginPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install plugin: %w", err)
	}

	fmt.Printf("  Plugin updated at %s\n", pluginPath)
	return nil
}

// fetchLatestTag returns the latest stable tag from GitHub API.
func fetchLatestTag() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/kiosvantra/metronous/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", fmt.Errorf("no releases found")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Simple JSON extraction: "tag_name":"v0.9.13"
	s := string(body)
	idx := strings.Index(s, `"tag_name"`)
	if idx == -1 {
		return "", fmt.Errorf("tag_name not found in GitHub API response")
	}
	rest := s[idx+len(`"tag_name"`):]
	colon := strings.Index(rest, `"`)
	if colon == -1 {
		return "", fmt.Errorf("malformed tag_name in GitHub API response")
	}
	rest = rest[colon+1:]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return "", fmt.Errorf("malformed tag_name in GitHub API response")
	}
	tag := rest[:end]
	if tag == "" {
		return "", fmt.Errorf("empty tag_name in GitHub API response")
	}
	return tag, nil
}

// downloadAndInstallBinary downloads a .tar.gz asset, extracts the metronous
// binary from it, and atomically replaces destPath.
func downloadAndInstallBinary(url, destPath string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed with status %d from %s", resp.StatusCode, url)
	}

	// Decompress and extract binary from tar.gz
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read gzip stream: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	binaryName := "metronous"
	if runtime.GOOS == "windows" {
		binaryName = "metronous.exe"
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), "metronous-update-*")
	if err != nil {
		// If we cannot write next to the destination (common on Linux for non-root installs),
		// fall back to a user-writable cache/temp directory.
		cacheDir, cacheErr := os.UserCacheDir()
		if cacheErr == nil {
			tmpFile, err = os.CreateTemp(cacheDir, "metronous-update-*")
		}
		if err != nil {
			tmpFile, err = os.CreateTemp(os.TempDir(), "metronous-update-*")
		}
		if err != nil {
			// Keep original error context.
			return fmt.Errorf("failed to create temp file: %w", err)
		}
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			tmpFile.Close()
			return fmt.Errorf("error reading archive: %w", err)
		}
		// Match the binary at the root of the archive (no directory prefix).
		if filepath.Base(hdr.Name) == binaryName && hdr.Typeflag == tar.TypeReg {
			if _, err := io.Copy(tmpFile, tr); err != nil {
				tmpFile.Close()
				return fmt.Errorf("failed to extract binary: %w", err)
			}
			found = true
			break
		}
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to flush temp file: %w", err)
	}
	if !found {
		return fmt.Errorf("binary %q not found in archive %s", binaryName, url)
	}

	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic replace.
	if err := os.Rename(tmpPath, destPath); err != nil {
		// Cross-volume fallback.
		if copyErr := copyFile(tmpPath, destPath); copyErr != nil {
			return fmt.Errorf("rename failed (%v) and copy also failed: %w", err, copyErr)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0755)
}
