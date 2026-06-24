package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

var regexpUUID = regexp.MustCompile(`"uuid":"[^"]*"`)

// TestProxyOnly runs a stored node through a local SOCKS inbound (no TUN, no root)
// and curls through it, to isolate "proxy passes traffic" from "TUN config".
// Pick the node by name substring via FREESURF_NODE (default: first node).
//
//	FREESURF_PROXYTEST=1 FREESURF_NODE=инлянд go test -run TestProxyOnly -v
func TestProxyOnly(t *testing.T) {
	if os.Getenv("FREESURF_PROXYTEST") == "" {
		t.Skip("set FREESURF_PROXYTEST=1 to run the live proxy test")
	}
	dir, err := appDataDir()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dir+"/freesurf.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	q := "SELECT name, uri FROM nodes"
	args := []any{}
	if sub := os.Getenv("FREESURF_NODE"); sub != "" {
		q += " WHERE name LIKE ?"
		args = append(args, "%"+sub+"%")
	}
	q += " ORDER BY id LIMIT 1"
	var name, uri string
	if err := db.QueryRow(q, args...).Scan(&name, &uri); err != nil {
		t.Fatalf("read node: %v", err)
	}
	t.Logf("testing node %q", name)

	bin, _ := singboxPath()
	tlsOf := func(o map[string]any) map[string]any { return o["tls"].(map[string]any) }
	trOf := func(o map[string]any) map[string]any { return o["transport"].(map[string]any) }

	run := func(label string, fn func(map[string]any)) (string, string) {
		outbound, err := parseVLESSOutbound(uri, "proxy")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fn != nil {
			fn(outbound)
		}
		cfg := map[string]any{
			"log":       map[string]any{"level": "error"},
			"inbounds":  []any{map[string]any{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": 1080}},
			"outbounds": []any{outbound, map[string]any{"type": "direct", "tag": "direct"}},
			"route":     map[string]any{"final": "proxy", "default_domain_resolver": "cf"},
			"dns":       map[string]any{"servers": []any{map[string]any{"type": "udp", "tag": "cf", "server": "1.1.1.1", "server_port": 53}}},
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.WriteFile("/tmp/fs-proxytest.json", data, 0644)

		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "run", "-c", "/tmp/fs-proxytest.json")
		logBuf := &strings.Builder{}
		cmd.Stdout, cmd.Stderr = logBuf, logBuf
		_ = cmd.Start()
		defer func() { _ = cmd.Process.Kill() }()
		time.Sleep(3 * time.Second)
		out, _ := exec.CommandContext(ctx, "curl", "-s", "--max-time", "12",
			"--socks5-hostname", "127.0.0.1:1080", "https://api.ipify.org").CombinedOutput()
		return strings.TrimSpace(string(out)), strings.TrimSpace(logBuf.String())
	}

	variants := []struct {
		name string
		fn   func(map[string]any)
	}{
		{"as-parsed(h3)", nil},
		{"no-alpn", func(o map[string]any) { delete(tlsOf(o), "alpn") }},
		{"alpn-h2", func(o map[string]any) { tlsOf(o)["alpn"] = []any{"h2"} }},
		{"alpn-h2+h1", func(o map[string]any) { tlsOf(o)["alpn"] = []any{"h2", "http/1.1"} }},
		{"no-alpn+host=sni", func(o map[string]any) {
			delete(tlsOf(o), "alpn")
			trOf(o)["host"] = tlsOf(o)["server_name"]
		}},
		{"mode=stream-one,no-alpn", func(o map[string]any) {
			delete(tlsOf(o), "alpn")
			trOf(o)["mode"] = "stream-one"
		}},
	}
	for _, v := range variants {
		ip, logs := run(v.name, v.fn)
		if ip != "" {
			t.Logf(">>> [%s] WORKS — exit IP %s", v.name, ip)
			return
		}
		errLine := ""
		for _, l := range strings.Split(logs, "\n") {
			if strings.Contains(l, "ERROR") {
				errLine = l
				break
			}
		}
		t.Logf("[%s] failed: %s", v.name, errLine)
	}
	t.Fatal("no xhttp variant passed traffic")
}
