package main

import (
	"database/sql"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestXrayConnects builds an Xray config from a stored node (via the real
// buildXrayOutbound/writeXrayConfig path), runs Xray, and curls through its SOCKS
// port — validating the Xray config builder against the real server (no TUN/root).
// Pick the node with FREESURF_NODE (substring of its name); default = first.
//
//	FREESURF_PROXYTEST=1 [FREESURF_NODE=инлянд] go test -run TestXrayConnects -v
func TestXrayConnects(t *testing.T) {
	if os.Getenv("FREESURF_PROXYTEST") == "" {
		t.Skip("set FREESURF_PROXYTEST=1 to run the live Xray test")
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
	var name string
	node := &Node{}
	if err := db.QueryRow(q, args...).Scan(&name, &node.URI); err != nil {
		t.Fatalf("read node: %v", err)
	}
	t.Logf("node: %q", name)

	cfgPath, err := writeXrayConfig(node)
	if err != nil {
		t.Fatalf("writeXrayConfig: %v", err)
	}
	bin, _ := xrayPath()
	logPath, _ := xrayLogPath()
	cmd, err := runXray(bin, cfgPath, logPath)
	if err != nil {
		t.Fatalf("runXray: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	time.Sleep(3 * time.Second)

	out, err := exec.Command("curl", "-s", "--max-time", "12",
		"--socks5-hostname", "127.0.0.1:10808", "https://api.ipify.org").CombinedOutput()
	ip := strings.TrimSpace(string(out))
	t.Logf("exit IP: %q (err=%v)", ip, err)
	if ip == "" {
		data, _ := os.ReadFile(logPath)
		t.Fatalf("Xray did not pass traffic. xray.log:\n%s", string(data))
	}
	t.Logf(">>> Xray connected, exit IP %s", ip)
}
