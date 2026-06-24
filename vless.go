package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseVLESSOutbound converts a `vless://` share URI into a sing-box outbound
// object (as a generic map ready for JSON encoding). It covers the common cases:
// TLS / Reality / no-TLS, uTLS fingerprint, ALPN, flow, and ws/grpc/http(upgrade)
// transports. Other protocols are not handled yet (VLESS-only milestone).
func parseVLESSOutbound(uri, tag string) (map[string]any, error) {
	if !strings.HasPrefix(uri, "vless://") {
		return nil, fmt.Errorf("not a vless:// link")
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid vless URI: %w", err)
	}

	uuid := u.User.Username()
	host := u.Hostname()
	if uuid == "" || host == "" {
		return nil, fmt.Errorf("vless link missing uuid or host")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		return nil, fmt.Errorf("vless link has invalid port %q", u.Port())
	}

	q := u.Query()

	out := map[string]any{
		"type":        "vless",
		"tag":         tag,
		"server":      host,
		"server_port": port,
		"uuid":        uuid,
	}

	if flow := q.Get("flow"); flow != "" {
		out["flow"] = flow
	}
	if pe := q.Get("packetEncoding"); pe != "" {
		out["packet_encoding"] = pe
	}

	if tls := buildTLS(q, host); tls != nil {
		out["tls"] = tls
	}
	if tr := buildTransport(q); tr != nil {
		out["transport"] = tr
	}

	return out, nil
}

// buildTLS returns the sing-box tls block, or nil when security is "none"/absent.
func buildTLS(q url.Values, host string) map[string]any {
	security := q.Get("security")
	pbk := q.Get("pbk")
	// Reality implies TLS even if "security" is missing.
	if security == "none" || (security == "" && pbk == "") {
		return nil
	}

	tls := map[string]any{"enabled": true}

	sni := q.Get("sni")
	if sni == "" {
		sni = q.Get("peer")
	}
	if sni == "" {
		sni = host
	}
	tls["server_name"] = sni

	if ins := q.Get("allowInsecure"); ins == "1" || ins == "true" {
		tls["insecure"] = true
	} else if ins := q.Get("insecure"); ins == "1" || ins == "true" {
		tls["insecure"] = true
	}

	if alpn := q.Get("alpn"); alpn != "" {
		tls["alpn"] = splitCSV(alpn)
	}

	if fp := q.Get("fp"); fp != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}

	if pbk != "" {
		reality := map[string]any{"enabled": true, "public_key": pbk}
		if sid := q.Get("sid"); sid != "" {
			reality["short_id"] = sid
		}
		tls["reality"] = reality
		// Reality requires uTLS; default to chrome if no fingerprint was given.
		if _, ok := tls["utls"]; !ok {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
		}
	}

	return tls
}

// buildTransport returns the sing-box transport block, or nil for plain TCP.
func buildTransport(q url.Values) map[string]any {
	switch q.Get("type") {
	case "ws":
		tr := map[string]any{"type": "ws"}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		if h := q.Get("host"); h != "" {
			tr["headers"] = map[string]any{"Host": h}
		}
		return tr
	case "grpc":
		tr := map[string]any{"type": "grpc"}
		if s := q.Get("serviceName"); s != "" {
			tr["service_name"] = s
		}
		return tr
	case "http":
		tr := map[string]any{"type": "http"}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		if h := q.Get("host"); h != "" {
			tr["host"] = splitCSV(h)
		}
		return tr
	case "httpupgrade":
		tr := map[string]any{"type": "httpupgrade"}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		if h := q.Get("host"); h != "" {
			tr["host"] = h
		}
		return tr
	case "xhttp":
		// Xray "xhttp" (splithttp). Requires a core built with_xhttp — the
		// sing-box-lx fork we pin provides it.
		tr := map[string]any{"type": "xhttp"}
		if mode := q.Get("mode"); mode != "" {
			tr["mode"] = mode
		}
		if path := q.Get("path"); path != "" {
			tr["path"] = path
		}
		if h := q.Get("host"); h != "" {
			tr["host"] = h
		}
		return tr
	default:
		return nil
	}
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
