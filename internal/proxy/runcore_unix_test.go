//go:build !windows

package proxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Xray's log stays with its owner; sing-box's is read by the unprivileged app.
func TestCoreLogPermissions(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "core")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")

	tests := []struct {
		name string
		run  func(string, string, string) (*Process, error)
		want os.FileMode
	}{
		{name: "xray", run: RunXray, want: 0600},
		{name: "singbox", run: RunSingbox, want: 0644},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(dir, tc.name+".log")
			p, err := tc.run(bin, cfg, logPath)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			defer p.Stop(time.Second)

			info, err := os.Stat(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tc.want {
				t.Errorf("log written %o, want %o", got, tc.want)
			}
		})
	}
}

// A log left over from an earlier run must not keep its old, looser mode.
func TestCoreLogPermissionsOnReuse(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "core")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "xray.log")
	if err := os.WriteFile(logPath, []byte("stale\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := RunXray(bin, filepath.Join(dir, "config.json"), logPath)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	defer p.Stop(time.Second)

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("reused log kept mode %o, want 600", got)
	}
}

// The Xray config carries the VLESS UUID, so rewriting one must narrow its mode.
func TestWriteJSONTightensAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeJSON(map[string]any{"a": 1}, func() (string, error) { return path, nil }); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("config left at mode %o, want 600", perm)
	}
}
