package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestLoadListenAddressDefaultsToLoopbackWithoutLANOptIn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`version: "1"
server:
  listen_address: "0.0.0.0:8080"
  enable_timeline_lan: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	prog := &Program{cfg: Config{DataDir: filepath.Join(dir, "data")}, logger: zap.NewNop()}
	if got := prog.loadListenAddress(); got != "127.0.0.1:0" {
		t.Fatalf("got %q want 127.0.0.1:0", got)
	}
}
