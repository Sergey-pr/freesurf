//go:build !windows

package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// On Windows the request file is guarded by the ProgramData DACL instead.
func TestWriteRequestIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.run")
	if err := writeRequest(path, tunnelRequest{Nonce: "0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("request written with mode %o, want 600", perm)
	}
}

// Rewriting a world-readable request has to narrow it, which os.WriteFile will not.
func TestWriteRequestTightensAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.run")
	if err := os.WriteFile(path, []byte("run\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeRequest(path, tunnelRequest{Nonce: "0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("request left at mode %o, want 600", perm)
	}
}
