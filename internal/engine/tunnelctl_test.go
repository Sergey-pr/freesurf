package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Anything the root supervisor cannot vouch for must read as "no request".
func TestReadRequestRejectsUntrustedInput(t *testing.T) {
	const goodNonce = "0123456789abcdef"
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "valid with IP", raw: `{"nonce":"` + goodNonce + `","serverIP":"192.0.2.1"}`, want: true},
		{name: "valid without IP", raw: `{"nonce":"` + goodNonce + `"}`, want: true},
		{name: "not JSON", raw: "run\n"},
		{name: "empty", raw: ""},
		{name: "no nonce", raw: `{"serverIP":"192.0.2.1"}`},
		{name: "nonce too short", raw: `{"nonce":"abc"}`},
		{name: "nonce not hex", raw: `{"nonce":"../../etc/passwd0"}`},
		{name: "nonce upper case", raw: `{"nonce":"0123456789ABCDEF"}`},
		{name: "serverIP is a hostname", raw: `{"nonce":"` + goodNonce + `","serverIP":"evil.example.com"}`},
		{name: "serverIP is a command", raw: `{"nonce":"` + goodNonce + `","serverIP":"1.2.3.4; rm -rf /"}`},
		{name: "serverIP is a CIDR", raw: `{"nonce":"` + goodNonce + `","serverIP":"0.0.0.0/0"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tunnel.run")
			if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, ok := readRequest(path); ok != tc.want {
				t.Fatalf("readRequest accepted=%v, want %v", ok, tc.want)
			}
		})
	}
}

func TestReadRequestMissingFile(t *testing.T) {
	if _, ok := readRequest(filepath.Join(t.TempDir(), "absent")); ok {
		t.Fatal("a missing request read as present")
	}
}

func TestRequestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel.run")
	nonce, err := newNonce()
	if err != nil {
		t.Fatal(err)
	}
	if !nonceRe.MatchString(nonce) {
		t.Fatalf("newNonce produced %q, which the supervisor would reject", nonce)
	}
	if err := writeRequest(path, tunnelRequest{Nonce: nonce, ServerIP: "198.51.100.7"}); err != nil {
		t.Fatal(err)
	}
	got, ok := readRequest(path)
	if !ok {
		t.Fatal("a request we wrote did not read back")
	}
	if got.Nonce != nonce || got.ServerIP != "198.51.100.7" {
		t.Fatalf("read back %+v, want nonce %s and IP 198.51.100.7", got, nonce)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("request written with mode %o, want 600", perm)
	}
}

func TestNoncesDiffer(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		n, err := newNonce()
		if err != nil {
			t.Fatal(err)
		}
		if seen[n] {
			t.Fatalf("newNonce repeated %q", n)
		}
		seen[n] = true
	}
}

func TestStatusRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	want := tunnelStatus{Nonce: "0123456789abcdef", State: tunnelFailed, Message: "bind: permission denied"}
	if err := writeStatus(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := readStatus(path)
	if !ok {
		t.Fatal("status did not read back")
	}
	if got != want {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
	if err := got.err(); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err() = %v, want the supervisor's message", err)
	}

	// Rewriting must leave no temp files behind for the app to trip over.
	if err := writeStatus(path, tunnelStatus{Nonce: "fedcba9876543210", State: tunnelRunning}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("status directory holds %d files, want just the status", len(entries))
	}
}

func TestStatusErrWithoutMessage(t *testing.T) {
	st := tunnelStatus{State: tunnelFailed}
	if err := st.err(); err == nil {
		t.Fatal("a failed status produced no error")
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
