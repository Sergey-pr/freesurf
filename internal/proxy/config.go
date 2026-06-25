package proxy

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"freesurf/internal/paths"
	"freesurf/internal/store"
)

// WriteSingboxConfig writes the node-independent sing-box config (TUN in → local
// SOCKS out to Xray) and returns its path. Node-specific details live in the Xray
// config. Xray's own traffic to the real server is matched by process name and
// sent out directly, which breaks the routing loop.
func WriteSingboxConfig() (string, error) {
	cfg := map[string]any{
		// No "output": the launchd daemon redirects sing-box's stderr to the log
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
				"strict_route": false, // on macOS, true loops our outbound back into the TUN
				"stack":        "system",
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
			"rules": []any{
				map[string]any{"inbound": "tun-in", "action": "sniff"},
				map[string]any{"protocol": "dns", "action": "hijack-dns"},
				map[string]any{"process_name": []any{paths.XrayName}, "outbound": "direct"},
				map[string]any{"ip_is_private": true, "outbound": "direct"},
			},
		},
	}
	return writeJSON(cfg, paths.Config)
}

// WriteXrayConfig builds the Xray config (SOCKS in + the node's outbound) and
// writes it, returning its path.
func WriteXrayConfig(node *store.Node) (string, error) {
	outbound, err := buildXrayOutbound(node.URI)
	if err != nil {
		return "", err
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
	return writeJSON(cfg, paths.XrayConfig)
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
func buildXrayOutbound(uri string) (map[string]any, error) {
	if !strings.HasPrefix(uri, "vless://") {
		return nil, fmt.Errorf("only vless:// links are supported for now")
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid vless URI: %w", err)
	}
	uuid, host := u.User.Username(), u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if uuid == "" || host == "" || port <= 0 {
		return nil, fmt.Errorf("vless link missing uuid/host/port")
	}
	q := u.Query()

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
			"address": host, "port": port, "users": []any{user},
		}}},
		"streamSettings": stream,
	}, nil
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
