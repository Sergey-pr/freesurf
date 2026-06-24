package main

import (
	"encoding/json"
	"os"
)

// buildSingboxConfig assembles a sing-box config that captures all device traffic
// via TUN and forwards it to the local Xray SOCKS server (which speaks the actual
// proxy protocol). Xray's own traffic to the real server is matched by process
// name and sent out directly, breaking the routing loop.
//
// Schema targets the pinned sing-box 1.13.x (TUN `address[]`, route actions).
func buildSingboxConfig(socksPort int) map[string]any {
	return map[string]any{
		// No "output": the launchd daemon redirects sing-box's stderr to the log
		// file (single writer), so we don't also open it from inside the core.
		"log": map[string]any{
			"level":     "info",
			"timestamp": true,
		},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{
					"type":        "https",
					"tag":         "proxy-dns",
					"server":      "8.8.8.8",
					"server_port": 443,
					"path":        "/dns-query",
					"detour":      "proxy",
				},
				map[string]any{
					"type":        "udp",
					"tag":         "local-dns",
					"server":      "1.1.1.1",
					"server_port": 53,
				},
			},
			"rules": []any{
				map[string]any{"server": "proxy-dns"},
			},
			"final":    "proxy-dns",
			"strategy": "prefer_ipv4",
		},
		"inbounds": []any{
			map[string]any{
				"type":         "tun",
				"tag":          "tun-in",
				"address":      []any{"172.18.0.1/30"},
				"mtu":          1492,
				"auto_route":   true,
				"strict_route": false,
				"stack":        "system",
			},
		},
		"outbounds": []any{
			// All proxied traffic goes to the local Xray SOCKS server.
			map[string]any{
				"type":        "socks",
				"tag":         "proxy",
				"server":      "127.0.0.1",
				"server_port": socksPort,
				"version":     "5",
			},
			map[string]any{"type": "direct", "tag": "direct"},
		},
		"route": map[string]any{
			"default_domain_resolver": "local-dns",
			"auto_detect_interface":   true,
			"final":                   "proxy",
			"rules": []any{
				map[string]any{"inbound": "tun-in", "action": "sniff"},
				map[string]any{"protocol": "dns", "action": "hijack-dns"},
				// Xray's own connection to the real server must bypass the TUN,
				// or it loops back into this proxy. Match it by process name.
				map[string]any{"process_name": []any{xrayExecName}, "outbound": "direct"},
				map[string]any{"ip_is_private": true, "outbound": "direct"},
			},
		},
	}
}

// writeSingboxConfig writes the (node-independent) sing-box TUN→SOCKS config and
// returns its path. The node-specific details live in the Xray config.
func writeSingboxConfig(socksPort int) (string, error) {
	cfg := buildSingboxConfig(socksPort)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	path, err := configPath()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}
