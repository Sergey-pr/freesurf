package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestDecryptHappLink decrypts a real happ://crypt5/ link read from a file
// (path in FREESURF_HAPP_FILE) so the subscription stays out of the repo.
//
//	FREESURF_HAPP_FILE=/tmp/happ_link.txt go test -run TestDecryptHappLink -v
func TestDecryptHappLink(t *testing.T) {
	path := os.Getenv("FREESURF_HAPP_FILE")
	if path == "" {
		t.Skip("set FREESURF_HAPP_FILE to a file containing a happ://crypt5/ link")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptHapp(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	t.Logf("decrypted (%d bytes):\n%s", len(plain), plain)
}

// TestHappEndToEnd decrypts the link, fetches the subscription and parses nodes.
//
//	FREESURF_HAPP_FILE=/tmp/happ_link.txt go test -run TestHappEndToEnd -v
func TestHappEndToEnd(t *testing.T) {
	path := os.Getenv("FREESURF_HAPP_FILE")
	if path == "" {
		t.Skip("set FREESURF_HAPP_FILE to a file containing a happ://crypt5/ link")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	parsed, err := buildImport(ctx, strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("buildImport: %v", err)
	}
	t.Logf("server %q (kind=%s) with %d nodes", parsed.Name, parsed.Kind, len(parsed.Nodes))
	for _, n := range parsed.Nodes {
		t.Logf("  - %-22s %s", n.Name, n.Protocol)
	}
	if len(parsed.Nodes) == 0 {
		t.Fatal("expected at least one node")
	}
}

func TestHappShufflesAreInvolutions(t *testing.T) {
	in := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
	if got := string(happPermute4(happPermute4(in))); got != string(in) {
		t.Errorf("permute4 not involution: %q", got)
	}
	if got := string(happSwapPairs(happSwapPairs(in))); got != string(in) {
		t.Errorf("swapPairs not involution: %q", got)
	}
}
