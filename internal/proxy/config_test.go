package proxy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"freesurf/internal/paths"
)

// noResolve keeps the tests off the network; the resolver is exercised separately.
func noResolve(string) string { return "" }

func pinned(ip string) func(string) string {
	return func(string) string { return ip }
}

// outboundOf builds an outbound and fails the test if the URI was rejected.
func outboundOf(t *testing.T, uri string) map[string]any {
	t.Helper()
	out, _, err := buildXrayOutbound(uri, noResolve)
	if err != nil {
		t.Fatalf("buildXrayOutbound(%q): %v", uri, err)
	}
	return out
}

// stream returns the outbound's streamSettings.
func stream(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	s, ok := out["streamSettings"].(map[string]any)
	if !ok {
		t.Fatalf("outbound has no streamSettings: %#v", out)
	}
	return s
}

// vnext returns the single server entry of a vless outbound.
func vnext(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	settings := out["settings"].(map[string]any)
	list := settings["vnext"].([]any)
	if len(list) != 1 {
		t.Fatalf("want one vnext entry, got %d", len(list))
	}
	return list[0].(map[string]any)
}

func TestBuildXrayOutboundRejects(t *testing.T) {
	tests := []struct {
		name, uri, wantErr string
	}{
		{name: "empty", uri: "", wantErr: "only vless"},
		{name: "wrong scheme", uri: "vmess://uuid@example.com:443", wantErr: "only vless"},
		{name: "trojan", uri: "trojan://pass@example.com:443", wantErr: "only vless"},
		{name: "unparseable", uri: "vless://%zz@example.com:443", wantErr: "invalid vless URI"},
		{name: "no uuid", uri: "vless://example.com:443", wantErr: "missing uuid/host/port"},
		{name: "no port", uri: "vless://uuid@example.com", wantErr: "missing uuid/host/port"},
		{name: "port zero", uri: "vless://uuid@example.com:0", wantErr: "missing uuid/host/port"},
		{name: "port not a number", uri: "vless://uuid@example.com:https", wantErr: "invalid vless URI"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildXrayOutbound(tc.uri, noResolve)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuildXrayOutboundServer(t *testing.T) {
	out := outboundOf(t, "vless://the-uuid@example.com:8443?flow=xtls-rprx-vision")

	if out["protocol"] != "vless" || out["tag"] != "proxy" {
		t.Errorf("got protocol %v tag %v, want vless/proxy", out["protocol"], out["tag"])
	}
	v := vnext(t, out)
	if v["address"] != "example.com" {
		t.Errorf("address = %v, want the host", v["address"])
	}
	if v["port"] != 8443 {
		t.Errorf("port = %v, want 8443", v["port"])
	}
	user := v["users"].([]any)[0].(map[string]any)
	if user["id"] != "the-uuid" {
		t.Errorf("id = %v, want the uuid", user["id"])
	}
	if user["encryption"] != "none" {
		t.Errorf("encryption = %v, want none", user["encryption"])
	}
	if user["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow = %v, want it carried through", user["flow"])
	}
}

// flow is omitted rather than sent empty, which the core would reject.
func TestBuildXrayOutboundWithoutFlow(t *testing.T) {
	out := outboundOf(t, "vless://uuid@example.com:443")
	user := vnext(t, out)["users"].([]any)[0].(map[string]any)
	if _, present := user["flow"]; present {
		t.Errorf("flow present without one in the URI: %#v", user)
	}
}

func TestBuildXrayOutboundTransports(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantNetwork string
		settingsKey string
		want        map[string]any
	}{
		{
			name: "tcp is the default", uri: "vless://u@h:443",
			wantNetwork: "tcp",
		},
		{
			name: "ws with path and host", uri: "vless://u@h:443?type=ws&path=%2Fws&host=cdn.example.com",
			wantNetwork: "ws", settingsKey: "wsSettings",
			want: map[string]any{"path": "/ws", "host": "cdn.example.com"},
		},
		{
			name: "grpc", uri: "vless://u@h:443?type=grpc&serviceName=gun",
			wantNetwork: "grpc", settingsKey: "grpcSettings",
			want: map[string]any{"serviceName": "gun"},
		},
		{
			name: "httpupgrade", uri: "vless://u@h:443?type=httpupgrade&path=%2Fhu",
			wantNetwork: "httpupgrade", settingsKey: "httpupgradeSettings",
			want: map[string]any{"path": "/hu"},
		},
		{
			name: "xhttp with mode", uri: "vless://u@h:443?type=xhttp&path=%2Fx&mode=stream-one",
			wantNetwork: "xhttp", settingsKey: "xhttpSettings",
			want: map[string]any{"path": "/x", "mode": "stream-one"},
		},
		{
			name: "splithttp is xhttp", uri: "vless://u@h:443?type=splithttp&path=%2Fx",
			wantNetwork: "xhttp", settingsKey: "xhttpSettings",
			want: map[string]any{"path": "/x"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := stream(t, outboundOf(t, tc.uri))
			if st["network"] != tc.wantNetwork {
				t.Fatalf("network = %v, want %v", st["network"], tc.wantNetwork)
			}
			if tc.settingsKey == "" {
				return
			}
			got := st[tc.settingsKey]
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s = %#v, want %#v", tc.settingsKey, got, tc.want)
			}
		})
	}
}

func TestBuildXrayOutboundSecurity(t *testing.T) {
	t.Run("reality wins on pbk alone", func(t *testing.T) {
		st := stream(t, outboundOf(t, "vless://u@h:443?security=tls&pbk=abc&sid=ff01&spx=%2F"))
		if st["security"] != "reality" {
			t.Fatalf("security = %v, want reality", st["security"])
		}
		r := st["realitySettings"].(map[string]any)
		want := map[string]any{"serverName": "h", "publicKey": "abc", "fingerprint": "chrome", "shortId": "ff01", "spiderX": "/"}
		if !reflect.DeepEqual(r, want) {
			t.Errorf("realitySettings = %#v, want %#v", r, want)
		}
	})

	t.Run("tls", func(t *testing.T) {
		st := stream(t, outboundOf(t, "vless://u@h:443?security=tls&sni=s.example.com&alpn=h3%2C%20h2&fp=firefox&allowInsecure=1"))
		if st["security"] != "tls" {
			t.Fatalf("security = %v, want tls", st["security"])
		}
		tls := st["tlsSettings"].(map[string]any)
		want := map[string]any{
			"serverName": "s.example.com", "fingerprint": "firefox",
			"alpn": []string{"h3", "h2"}, "allowInsecure": true,
		}
		if !reflect.DeepEqual(tls, want) {
			t.Errorf("tlsSettings = %#v, want %#v", tls, want)
		}
	})

	t.Run("insecure is the other spelling", func(t *testing.T) {
		st := stream(t, outboundOf(t, "vless://u@h:443?security=tls&insecure=1"))
		if st["tlsSettings"].(map[string]any)["allowInsecure"] != true {
			t.Error("insecure=1 did not set allowInsecure")
		}
	})

	t.Run("none", func(t *testing.T) {
		for _, uri := range []string{"vless://u@h:443", "vless://u@h:443?security=none"} {
			if st := stream(t, outboundOf(t, uri)); st["security"] != "none" {
				t.Errorf("%s: security = %v, want none", uri, st["security"])
			}
		}
	})

	t.Run("unknown value is passed through", func(t *testing.T) {
		if st := stream(t, outboundOf(t, "vless://u@h:443?security=xtls")); st["security"] != "xtls" {
			t.Errorf("security = %v, want it carried through", st["security"])
		}
	})
}

func TestServerNameFallback(t *testing.T) {
	tests := []struct{ name, uri, want string }{
		{name: "sni wins", uri: "vless://u@h:443?security=tls&sni=a&peer=b", want: "a"},
		{name: "peer next", uri: "vless://u@h:443?security=tls&peer=b", want: "b"},
		{name: "host last", uri: "vless://u@the.host:443?security=tls", want: "the.host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := stream(t, outboundOf(t, tc.uri))
			if got := st["tlsSettings"].(map[string]any)["serverName"]; got != tc.want {
				t.Errorf("serverName = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConnectAddress(t *testing.T) {
	tests := []struct {
		name, host, serverIP, goos, want string
	}{
		{name: "macOS keeps the domain", host: "example.com", serverIP: "192.0.2.1", goos: "darwin", want: "example.com"},
		{name: "Windows dials the pin", host: "example.com", serverIP: "192.0.2.1", goos: "windows", want: "192.0.2.1"},
		{name: "Windows without a pin", host: "example.com", serverIP: "", goos: "windows", want: "example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectAddress(tc.host, tc.serverIP, tc.goos); got != tc.want {
				t.Errorf("connectAddress = %q, want %q", got, tc.want)
			}
		})
	}
}

// The IP the outbound pins is the one handed to the supervisor for its direct rule.
func TestBuildXrayOutboundReturnsThePin(t *testing.T) {
	_, ip, err := buildXrayOutbound("vless://u@example.com:443", pinned("198.51.100.7"))
	if err != nil {
		t.Fatal(err)
	}
	if ip != "198.51.100.7" {
		t.Errorf("pinned IP = %q, want the resolver's answer", ip)
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{in: "h2", want: []string{"h2"}},
		{in: "h3, h2 ,http/1.1", want: []string{"h3", "h2", "http/1.1"}},
		{in: ",,", want: []string{}},
	}
	for _, tc := range tests {
		if got := splitCSV(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitCSV(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestTunOptionsFor(t *testing.T) {
	if stack, strict := tunOptionsFor("windows"); stack != "gvisor" || !strict {
		t.Errorf("windows = %q/%v, want gvisor/true", stack, strict)
	}
	if stack, strict := tunOptionsFor("darwin"); stack != "system" || strict {
		t.Errorf("darwin = %q/%v, want system/false", stack, strict)
	}
}

func TestXrayProcessNameFor(t *testing.T) {
	if got := xrayProcessNameFor("windows"); got != paths.XrayName+".exe" {
		t.Errorf("windows = %q, want the .exe suffix", got)
	}
	if got := xrayProcessNameFor("darwin"); got != paths.XrayName {
		t.Errorf("darwin = %q, want the bare name", got)
	}
}

// singboxDoc is the parsed shape of the generated sing-box config.
type singboxDoc struct {
	Route struct {
		Rules []map[string]any `json:"rules"`
		Final string           `json:"final"`
	} `json:"route"`
	Outbounds []map[string]any `json:"outbounds"`
	Inbounds  []map[string]any `json:"inbounds"`
}

func singboxConfigOf(t *testing.T, serverIP string) singboxDoc {
	t.Helper()
	data, err := SingboxConfig(serverIP)
	if err != nil {
		t.Fatalf("SingboxConfig(%q): %v", serverIP, err)
	}
	var doc singboxDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("generated config is not valid JSON: %v", err)
	}
	return doc
}

func TestSingboxConfigPinsTheServer(t *testing.T) {
	tests := []struct {
		name, serverIP, wantCIDR string
	}{
		{name: "ipv4", serverIP: "198.51.100.7", wantCIDR: "198.51.100.7/32"},
		{name: "ipv6", serverIP: "2001:db8::1", wantCIDR: "2001:db8::1/128"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := singboxConfigOf(t, tc.serverIP)
			first := doc.Route.Rules[0]
			cidrs, ok := first["ip_cidr"].([]any)
			if !ok {
				t.Fatalf("the pin is not the first rule: %#v", first)
			}
			if cidrs[0] != tc.wantCIDR {
				t.Errorf("ip_cidr = %v, want %v", cidrs[0], tc.wantCIDR)
			}
			if first["outbound"] != "direct" {
				t.Errorf("the pin routes to %v, want direct", first["outbound"])
			}
		})
	}
}

// Without a usable pin the rule is left out rather than emitted empty or malformed.
func TestSingboxConfigWithoutAPin(t *testing.T) {
	for _, serverIP := range []string{"", "not-an-ip", "example.com", "198.51.100.7/32"} {
		doc := singboxConfigOf(t, serverIP)
		for _, rule := range doc.Route.Rules {
			if _, present := rule["ip_cidr"]; present {
				t.Errorf("serverIP %q produced an ip_cidr rule: %#v", serverIP, rule)
			}
		}
	}
}

func TestSingboxConfigRouting(t *testing.T) {
	doc := singboxConfigOf(t, "198.51.100.7")

	if doc.Route.Final != "proxy" {
		t.Errorf("route.final = %q, want proxy", doc.Route.Final)
	}

	var sawProcessRule bool
	for _, rule := range doc.Route.Rules {
		names, ok := rule["process_name"].([]any)
		if !ok {
			continue
		}
		sawProcessRule = true
		if names[0] != xrayProcessName() {
			t.Errorf("process_name = %v, want %q", names[0], xrayProcessName())
		}
		if rule["outbound"] != "direct" {
			t.Errorf("the Xray rule routes to %v, want direct", rule["outbound"])
		}
	}
	if !sawProcessRule {
		t.Error("no process_name rule: Xray's own traffic would loop back into the tunnel")
	}

	proxy := doc.Outbounds[0]
	if proxy["type"] != "socks" || proxy["server"] != "127.0.0.1" {
		t.Errorf("first outbound = %#v, want the local SOCKS", proxy)
	}
	if port, _ := proxy["server_port"].(float64); int(port) != socksPort {
		t.Errorf("SOCKS port = %v, want %d", proxy["server_port"], socksPort)
	}

	tun := doc.Inbounds[0]
	stack, strict := tunOptions()
	if tun["stack"] != stack || tun["strict_route"] != strict {
		t.Errorf("tun = %v/%v, want %v/%v", tun["stack"], tun["strict_route"], stack, strict)
	}
	if tun["auto_route"] != true {
		t.Error("auto_route is off: the tunnel would carry no traffic")
	}
}
