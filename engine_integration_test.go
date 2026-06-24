package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestEngineConfigChecks downloads the real pinned cores and validates the
// generated sing-box TUN→SOCKS config with `sing-box check`. Gated behind
// FREESURF_E2E=1 (hits the network, writes to the app data dir).
//
//	FREESURF_E2E=1 go test -run TestEngineConfigChecks -v
func TestEngineConfigChecks(t *testing.T) {
	if os.Getenv("FREESURF_E2E") == "" {
		t.Skip("set FREESURF_E2E=1 to run the core download + sing-box check test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	bin, err := ensureCore(ctx)
	if err != nil {
		t.Fatalf("ensureCore: %v", err)
	}
	if _, err := ensureXray(ctx); err != nil {
		t.Fatalf("ensureXray: %v", err)
	}

	cfgPath, err := writeSingboxConfig(xraySocksPort)
	if err != nil {
		t.Fatalf("writeSingboxConfig: %v", err)
	}
	if err := checkConfig(bin, cfgPath); err != nil {
		t.Fatalf("sing-box check rejected the generated config: %v", err)
	}

	// Xray config from a representative xhttp node must be well-formed JSON the
	// builder accepts.
	node := &Node{URI: "vless://11111111-2222-3333-4444-555555555555@example.com:443?" +
		"security=tls&type=xhttp&mode=auto&path=%2F&alpn=h3&sni=front.example#Test"}
	if _, err := writeXrayConfig(node); err != nil {
		t.Fatalf("writeXrayConfig: %v", err)
	}
}
