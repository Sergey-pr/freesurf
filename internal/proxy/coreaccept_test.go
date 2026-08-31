package proxy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"freesurf/internal/paths"
	"freesurf/internal/store"
)

// nodeURIs covers the shapes real subscriptions hand us.
var nodeURIs = map[string]string{
	"tcp reality vision": "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=Sd_1yVvKcT0uZ8kKbHhqPvJ0jVQ4mYyOGVLQnZ8pRms&sid=ab12cd34&fp=chrome&sni=www.microsoft.com&flow=xtls-rprx-vision",
	"xhttp stream-one":   "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=xhttp&security=tls&path=%2Fx&mode=stream-one&sni=example.com&fp=chrome&alpn=h3%2Ch2",
	"xhttp auto":         "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=xhttp&security=tls&path=%2Fx&mode=auto&sni=example.com&fp=chrome",
	"ws":                 "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=ws&security=tls&path=%2Fws&host=cdn.example.com&sni=example.com",
	"grpc":               "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=grpc&security=tls&serviceName=gun&sni=example.com",
	"httpupgrade":        "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=httpupgrade&security=tls&path=%2Fhu&sni=example.com",
	"plain tcp":          "vless://11111111-2222-3333-4444-555555555555@example.com:443",
}

// installCore puts an embedded core in a temp dir, skipping when this build has none.
func installCore(t *testing.T, name string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), name)
	if err := installEmbeddedCore(name, dest); err != nil {
		t.Skipf("cores not embedded in this test build: %v", err)
	}
	return dest
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A core bump or a new transport shows up here rather than at connect time.
func TestXrayAcceptsGeneratedConfig(t *testing.T) {
	bin := installCore(t, paths.XrayName)
	for name, uri := range nodeURIs {
		t.Run(name, func(t *testing.T) {
			cfg, _, err := XrayConfig(&store.Node{URI: uri}, noResolve)
			if err != nil {
				t.Fatalf("XrayConfig: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, bin, "run", "-test", "-c", writeTemp(t, cfg)).CombinedOutput()
			if err != nil {
				t.Fatalf("xray rejected the config: %v\n%s", err, out)
			}
		})
	}
}

func TestSingboxAcceptsGeneratedConfig(t *testing.T) {
	bin := installCore(t, paths.SingboxName)
	for _, serverIP := range []string{"", "198.51.100.7", "2001:db8::1"} {
		name := "no pin"
		if serverIP != "" {
			name = serverIP
		}
		t.Run(name, func(t *testing.T) {
			cfg, err := SingboxConfig(serverIP)
			if err != nil {
				t.Fatalf("SingboxConfig: %v", err)
			}
			if err := CheckConfig(bin, cfg); err != nil {
				t.Fatalf("sing-box rejected the config: %v", err)
			}
		})
	}
}
