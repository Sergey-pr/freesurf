package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"

	"freesurf/internal/dnsresolve"
	"freesurf/internal/paths"
	"freesurf/internal/store"
)

// tunOptions returns the platform-tuned TUN stack and strict_route setting. On
// macOS the system stack works and strict_route must stay off (true loops our own
// outbound back into the TUN); on Windows the gvisor stack over Wintun with
// strict_route on is the reliable combination and avoids route/DNS leaks.
func tunOptions() (stack string, strictRoute bool) {
	if runtime.GOOS == "windows" {
		return "gvisor", true
	}
	return "system", false
}

// xrayProcessName is how sing-box's process_name rule sees the Xray process, used
// to send Xray's own traffic to the server out directly (breaking the routing
// loop). On Windows the process name includes the .exe suffix.
func xrayProcessName() string {
	if runtime.GOOS == "windows" {
		return paths.XrayName + ".exe"
	}
	return paths.XrayName
}

// resolveServerIP returns a concrete IPv4 for host (the host itself if it is
// already an IP), or "" if it can't be resolved. Resolution goes through
// dnsresolve (DoH + system) so a poisoned local DNS path can't hand back a
// blackholed address - on Windows this IP is the actual Xray connect address.
// Pinning a single IP - shared by the Xray outbound address and the sing-box
// direct rule - is what guarantees the two agree (a domain resolved twice can
// yield different IPs), which is what breaks the routing loop.
func resolveServerIP(host string) string {
	return dnsresolve.FirstIP(host)
}

func ipToCIDR(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

// WriteSingboxConfig writes the sing-box config (TUN in → local SOCKS out to Xray)
// and returns its path. serverIP is the pinned proxy-server address (from
// WriteXrayConfig) so Xray's own traffic to it can be routed out directly, breaking
// the routing loop.
func WriteSingboxConfig(serverIP string) (string, error) {
	stack, strictRoute := tunOptions()

	// Break the routing loop: Xray's connection to the proxy server must go out
	// directly, not back through the TUN. process_name handles this on macOS but is
	// unreliable on Windows (sing-box runs as a service and can't attribute Xray's
	// process), so we also exclude the exact server IP Xray connects to.
	routeRules := []any{
		map[string]any{"inbound": "tun-in", "action": "sniff"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"process_name": []any{xrayProcessName()}, "outbound": "direct"},
		map[string]any{"ip_is_private": true, "outbound": "direct"},
	}
	if ip := net.ParseIP(serverIP); ip != nil {
		serverRule := map[string]any{"ip_cidr": []any{ipToCIDR(ip)}, "outbound": "direct"}
		routeRules = append([]any{serverRule}, routeRules...)
	}

	cfg := map[string]any{
		// No "output": the privileged helper redirects sing-box's stderr to the log
		// file, so we don't also open it from inside the core.
		"log": map[string]any{"level": "info", "timestamp": true},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"type": "https", "tag": "proxy-dns", "server": "8.8.8.8", "server_port": 443, "path": "/dns-query", "detour": "proxy"},
				map[string]any{"type": "udp", "tag": "local-dns", "server": "1.1.1.1", "server_port": 53},
			},
			"rules":    []any{map[string]any{"server": "proxy-dns"}},
			"final":    "proxy-dns",
			"strategy": "prefer_ipv4",
		},
		"inbounds": []any{
			map[string]any{
				"type": "tun", "tag": "tun-in",
				"address":      []any{"172.18.0.1/30"},
				"mtu":          1492,
				"auto_route":   true,
				"strict_route": strictRoute,
				"stack":        stack,
			},
		},
		"outbounds": []any{
			map[string]any{"type": "socks", "tag": "proxy", "server": "127.0.0.1", "server_port": socksPort, "version": "5"},
			map[string]any{"type": "direct", "tag": "direct"},
		},
		"route": map[string]any{
			"default_domain_resolver": "local-dns",
			"auto_detect_interface":   true,
			"final":                   "proxy",
			"rules":                   routeRules,
		},
	}
	return writeJSON(cfg, paths.Config)
}

// WriteXrayConfig builds the Xray config (SOCKS in + the node's outbound) and
// writes it, returning its path and the pinned proxy-server IP (to feed into the
// sing-box direct rule). The IP may be empty if the host couldn't be resolved.
func WriteXrayConfig(node *store.Node) (string, string, error) {
	outbound, serverIP, err := buildXrayOutbound(node.URI)
	if err != nil {
		return "", "", err
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"dns": map[string]any{"servers": []any{"1.1.1.1", "8.8.8.8"}},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": socksPort, "protocol": "socks",
			"settings": map[string]any{"udp": true},
			"sniffing": map[string]any{"enabled": true, "destOverride": []any{"http", "tls", "quic"}},
		}},
		"outbounds": []any{outbound, map[string]any{"protocol": "freedom", "tag": "direct"}},
	}
	path, err := writeJSON(cfg, paths.XrayConfig)
	return path, serverIP, err
}

func writeJSON(v any, pathFn func() (string, error)) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	path, err := pathFn()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

// buildXrayOutbound converts a vless:// share URI into an Xray outbound, covering
// TLS / Reality / uTLS and the tcp / xhttp / ws / grpc / httpupgrade transports.
func buildXrayOutbound(uri string) (map[string]any, string, error) {
	if !strings.HasPrefix(uri, "vless://") {
		return nil, "", fmt.Errorf("only vless:// links are supported for now")
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, "", fmt.Errorf("invalid vless URI: %w", err)
	}
	uuid, host := u.User.Username(), u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if uuid == "" || host == "" || port <= 0 {
		return nil, "", fmt.Errorf("vless link missing uuid/host/port")
	}
	q := u.Query()

	// Pin the server to a single resolved IP. On Windows we also use it as the
	// connect address (keeping the original host as SNI): sharing this exact IP with
	// the sing-box direct rule is what stops Xray's connection to the server from
	// looping back into the TUN, since process_name matching is unreliable there. On
	// macOS the domain is kept as-is (process_name breaks the loop), and the IP is
	// only used for the redundant, harmless direct rule.
	serverIP := resolveServerIP(host)
	addr := host
	if serverIP != "" && runtime.GOOS == "windows" {
		addr = serverIP
	}

	user := map[string]any{"id": uuid, "encryption": "none"}
	if flow := q.Get("flow"); flow != "" {
		user["flow"] = flow
	}

	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	stream := map[string]any{"network": network}
	switch network {
	case "ws":
		stream["wsSettings"] = pathHost(q)
	case "grpc":
		grpc := map[string]any{}
		if s := q.Get("serviceName"); s != "" {
			grpc["serviceName"] = s
		}
		stream["grpcSettings"] = grpc
	case "httpupgrade":
		stream["httpupgradeSettings"] = pathHost(q)
	case "xhttp", "splithttp":
		stream["network"] = "xhttp"
		xh := pathHost(q)
		if m := q.Get("mode"); m != "" {
			xh["mode"] = m
		}
		stream["xhttpSettings"] = xh
	}

	security := q.Get("security")
	if q.Get("pbk") != "" {
		security = "reality"
	}
	switch security {
	case "reality":
		stream["security"] = "reality"
		stream["realitySettings"] = realitySettings(q, host)
	case "tls":
		stream["security"] = "tls"
		stream["tlsSettings"] = tlsSettings(q, host)
	case "", "none":
		stream["security"] = "none"
	default:
		stream["security"] = security
	}

	return map[string]any{
		"protocol": "vless",
		"tag":      "proxy",
		"settings": map[string]any{"vnext": []any{map[string]any{
			"address": addr, "port": port, "users": []any{user},
		}}},
		"streamSettings": stream,
	}, serverIP, nil
}

func pathHost(q url.Values) map[string]any {
	m := map[string]any{}
	if p := q.Get("path"); p != "" {
		m["path"] = p
	}
	if h := q.Get("host"); h != "" {
		m["host"] = h
	}
	return m
}

func tlsSettings(q url.Values, host string) map[string]any {
	t := map[string]any{"serverName": sniOf(q, host), "fingerprint": fpOr(q, "chrome")}
	if a := q.Get("alpn"); a != "" {
		t["alpn"] = splitCSV(a)
	}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		t["allowInsecure"] = true
	}
	return t
}

func realitySettings(q url.Values, host string) map[string]any {
	r := map[string]any{"serverName": sniOf(q, host), "publicKey": q.Get("pbk"), "fingerprint": fpOr(q, "chrome")}
	if sid := q.Get("sid"); sid != "" {
		r["shortId"] = sid
	}
	if spx := q.Get("spx"); spx != "" {
		r["spiderX"] = spx
	}
	return r
}

func sniOf(q url.Values, host string) string {
	if sni := q.Get("sni"); sni != "" {
		return sni
	}
	if peer := q.Get("peer"); peer != "" {
		return peer
	}
	return host
}

func fpOr(q url.Values, def string) string {
	if fp := q.Get("fp"); fp != "" {
		return fp
	}
	return def
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
