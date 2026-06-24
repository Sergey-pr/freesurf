package main

import (
	"reflect"
	"testing"
)

func TestParseVLESS_Reality(t *testing.T) {
	uri := "vless://11111111-2222-3333-4444-555555555555@example.com:443?" +
		"security=reality&sni=www.microsoft.com&fp=chrome&pbk=ABCDEF&sid=00aa&" +
		"flow=xtls-rprx-vision#My%20Node"

	out, err := parseVLESSOutbound(uri, "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["server"] != "example.com" || out["server_port"] != 443 {
		t.Errorf("server/port wrong: %v %v", out["server"], out["server_port"])
	}
	if out["uuid"] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("uuid wrong: %v", out["uuid"])
	}
	if out["flow"] != "xtls-rprx-vision" {
		t.Errorf("flow wrong: %v", out["flow"])
	}
	tls, ok := out["tls"].(map[string]any)
	if !ok {
		t.Fatal("missing tls block")
	}
	if tls["server_name"] != "www.microsoft.com" {
		t.Errorf("sni wrong: %v", tls["server_name"])
	}
	reality, ok := tls["reality"].(map[string]any)
	if !ok || reality["public_key"] != "ABCDEF" || reality["short_id"] != "00aa" {
		t.Errorf("reality wrong: %v", tls["reality"])
	}
	utls, ok := tls["utls"].(map[string]any)
	if !ok || utls["fingerprint"] != "chrome" {
		t.Errorf("utls wrong: %v", tls["utls"])
	}
}

func TestParseVLESS_WSWithTLS(t *testing.T) {
	uri := "vless://uuid@host.example:8443?security=tls&type=ws&path=%2Fws&host=cdn.example&sni=cdn.example#WS"
	out, err := parseVLESSOutbound(uri, "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := out["transport"].(map[string]any)
	if !ok {
		t.Fatal("missing transport")
	}
	if tr["type"] != "ws" || tr["path"] != "/ws" {
		t.Errorf("ws transport wrong: %v", tr)
	}
	headers, ok := tr["headers"].(map[string]any)
	if !ok || headers["Host"] != "cdn.example" {
		t.Errorf("ws host header wrong: %v", tr["headers"])
	}
}

func TestParseVLESS_NoTLS(t *testing.T) {
	out, err := parseVLESSOutbound("vless://uuid@1.2.3.4:80?security=none&type=tcp#plain", "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out["tls"]; ok {
		t.Errorf("did not expect tls block: %v", out["tls"])
	}
	if _, ok := out["transport"]; ok {
		t.Errorf("did not expect transport for tcp: %v", out["transport"])
	}
}

func TestParseVLESS_GRPC(t *testing.T) {
	out, _ := parseVLESSOutbound("vless://uuid@h:443?security=tls&type=grpc&serviceName=mygrpc#g", "proxy")
	tr, _ := out["transport"].(map[string]any)
	if tr["type"] != "grpc" || tr["service_name"] != "mygrpc" {
		t.Errorf("grpc transport wrong: %v", tr)
	}
}

func TestParseVLESS_Invalid(t *testing.T) {
	if _, err := parseVLESSOutbound("trojan://x@h:443", "proxy"); err == nil {
		t.Fatal("expected error for non-vless link")
	}
	if _, err := parseVLESSOutbound("vless://@host:443", "proxy"); err == nil {
		t.Fatal("expected error for missing uuid")
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV("h2, http/1.1 ,")
	want := []string{"h2", "http/1.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitCSV = %v, want %v", got, want)
	}
}
