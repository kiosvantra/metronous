package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDownloadAndInstallBinary_VerifiesChecksumAndInstalls(t *testing.T) {
	archive := makeTestArchive(t, []byte("new-binary"))
	sum := sha256.Sum256(archive)
	assetName := "metronous_1.2.3_linux_amd64.tar.gz"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive.tar.gz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archive)
		case "/checksums.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("%x  %s\n", sum, assetName)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "metronous")
	err := downloadAndInstallBinary(server.URL+"/archive.tar.gz", server.URL+"/checksums.txt", assetName, destPath)
	if err != nil {
		t.Fatalf("downloadAndInstallBinary returned error: %v", err)
	}

	installed, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(installed) != "new-binary" {
		t.Fatalf("unexpected installed content: %q", string(installed))
	}
}

func TestDownloadAndInstallBinary_FailsWhenChecksumMissing(t *testing.T) {
	archive := makeTestArchive(t, []byte("new-binary"))
	assetName := "metronous_1.2.3_linux_amd64.tar.gz"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive.tar.gz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archive)
		case "/checksums.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("deadbeef  some-other-file.tar.gz\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "metronous")
	err := downloadAndInstallBinary(server.URL+"/archive.tar.gz", server.URL+"/checksums.txt", assetName, destPath)
	if err == nil {
		t.Fatal("expected checksum verification to fail when checksum is missing")
	}
	if !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("expected checksum verification error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing checksum detail, got: %v", err)
	}

	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no installed binary on verification failure, stat err: %v", statErr)
	}
}

func TestDownloadAndInstallBinary_FailsWhenChecksumMismatch(t *testing.T) {
	archive := makeTestArchive(t, []byte("new-binary"))
	assetName := "metronous_1.2.3_linux_amd64.tar.gz"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive.tar.gz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archive)
		case "/checksums.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), assetName)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "metronous")
	err := downloadAndInstallBinary(server.URL+"/archive.tar.gz", server.URL+"/checksums.txt", assetName, destPath)
	if err == nil {
		t.Fatal("expected checksum verification to fail on mismatch")
	}
	if !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("expected checksum verification error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected mismatch detail, got: %v", err)
	}
}

func TestDownloadAndInstallBinary_FailsWhenChecksumsUnavailable(t *testing.T) {
	archive := makeTestArchive(t, []byte("new-binary"))
	assetName := "metronous_1.2.3_linux_amd64.tar.gz"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive.tar.gz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archive)
		case "/checksums.txt":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "metronous")
	err := downloadAndInstallBinary(server.URL+"/archive.tar.gz", server.URL+"/checksums.txt", assetName, destPath)
	if err == nil {
		t.Fatal("expected checksum verification to fail when checksums cannot be fetched")
	}
	if !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("expected checksum verification error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "checksums") {
		t.Fatalf("expected checksums fetch detail, got: %v", err)
	}
}

func makeTestArchive(t *testing.T, binary []byte) []byte {
	t.Helper()

	binaryName := "metronous"
	if runtime.GOOS == "windows" {
		binaryName = "metronous.exe"
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	hdr := &tar.Header{
		Name:     binaryName,
		Mode:     0o755,
		Size:     int64(len(binary)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	return buf.Bytes()
}
