package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// buildXrayOutbound converts a vless:// share URI into an Xray outbound object.
// Covers TLS / Reality / uTLS and the tcp / xhttp / ws / grpc / httpupgrade
// transports — including XHTTP, which is why we use Xray for these nodes.
func buildXrayOutbound(uri string) (map[string]any, error) {
	if !strings.HasPrefix(uri, "vless://") {
		return nil, fmt.Errorf("only vless:// links are supported for now")
	}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid vless URI: %w", err)
	}
	uuid := u.User.Username()
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	if uuid == "" || host == "" || port <= 0 {
		return nil, fmt.Errorf("vless link missing uuid/host/port")
	}
	q := u.Query()

	user := map[string]any{"id": uuid, "encryption": "none"}
	if flow := q.Get("flow"); flow != "" {
		user["flow"] = flow
	}

	stream := map[string]any{}
	network := q.Get("type")
	if network == "" {
		network = "tcp"
	}
	// Xray calls the splithttp transport "xhttp"; "raw" is its new name for tcp.
	stream["network"] = network

	switch network {
	case "ws":
		ws := map[string]any{}
		if p := q.Get("path"); p != "" {
			ws["path"] = p
		}
		if h := q.Get("host"); h != "" {
			ws["host"] = h
		}
		stream["wsSettings"] = ws
	case "grpc":
		grpc := map[string]any{}
		if s := q.Get("serviceName"); s != "" {
			grpc["serviceName"] = s
		}
		stream["grpcSettings"] = grpc
	case "httpupgrade":
		hu := map[string]any{}
		if p := q.Get("path"); p != "" {
			hu["path"] = p
		}
		if h := q.Get("host"); h != "" {
			hu["host"] = h
		}
		stream["httpupgradeSettings"] = hu
	case "xhttp", "splithttp":
		stream["network"] = "xhttp"
		xh := map[string]any{}
		if p := q.Get("path"); p != "" {
			xh["path"] = p
		}
		if h := q.Get("host"); h != "" {
			xh["host"] = h
		}
		if m := q.Get("mode"); m != "" {
			xh["mode"] = m
		}
		stream["xhttpSettings"] = xh
	}

	security := q.Get("security")
	pbk := q.Get("pbk")
	if pbk != "" {
		security = "reality"
	}
	switch security {
	case "reality":
		stream["security"] = "reality"
		stream["realitySettings"] = realitySettings(q, host)
	case "tls":
		stream["security"] = "tls"
		stream["tlsSettings"] = tlsSettings(q, host)
	case "none", "":
		stream["security"] = "none"
	default:
		stream["security"] = security
	}

	return map[string]any{
		"protocol": "vless",
		"tag":      "proxy",
		"settings": map[string]any{
			"vnext": []any{map[string]any{
				"address": host,
				"port":    port,
				"users":   []any{user},
			}},
		},
		"streamSettings": stream,
	}, nil
}

func tlsSettings(q url.Values, host string) map[string]any {
	t := map[string]any{"serverName": sniOf(q, host)}
	if a := q.Get("alpn"); a != "" {
		t["alpn"] = splitCSV(a)
	}
	t["fingerprint"] = fpOr(q, "chrome")
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		t["allowInsecure"] = true
	}
	return t
}

func realitySettings(q url.Values, host string) map[string]any {
	r := map[string]any{
		"serverName":  sniOf(q, host),
		"publicKey":   q.Get("pbk"),
		"fingerprint": fpOr(q, "chrome"),
	}
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

// writeXrayConfig builds the full Xray config (SOCKS inbound + node outbound +
// direct DNS) for the given node and writes it to disk, returning the path.
func writeXrayConfig(node *Node) (string, error) {
	outbound, err := buildXrayOutbound(node.URI)
	if err != nil {
		return "", err
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"dns": map[string]any{"servers": []any{"1.1.1.1", "8.8.8.8"}},
		"inbounds": []any{map[string]any{
			"listen":   "127.0.0.1",
			"port":     xraySocksPort,
			"protocol": "socks",
			"settings": map[string]any{"udp": true},
			"sniffing": map[string]any{"enabled": true, "destOverride": []any{"http", "tls", "quic"}},
		}},
		"outbounds": []any{
			outbound,
			map[string]any{"protocol": "freedom", "tag": "direct"},
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	path, err := xrayConfigPath()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}
