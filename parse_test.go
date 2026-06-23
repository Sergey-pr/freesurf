package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseImport_SingleSubscriptionURL(t *testing.T) {
	got, err := parseImport("https://example.com/sub?token=abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Kind != KindSubscription {
		t.Errorf("kind = %q, want %q", got.Kind, KindSubscription)
	}
	if got.URL == nil || *got.URL != "https://example.com/sub?token=abc" {
		t.Errorf("url = %v, want the subscription url", got.URL)
	}
	if got.Name != "example.com" {
		t.Errorf("name = %q, want host", got.Name)
	}
	if len(got.Nodes) != 0 {
		t.Errorf("nodes = %d, want 0 (fetched later)", len(got.Nodes))
	}
}

func TestParseImport_SingleShareURI(t *testing.T) {
	got, err := parseImport("vless://uuid@host.example:443?type=tcp#Tokyo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Kind != KindManual {
		t.Errorf("kind = %q, want %q", got.Kind, KindManual)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
	n := got.Nodes[0]
	if n.Protocol != "vless" || n.Name != "Tokyo" {
		t.Errorf("node = %+v, want protocol=vless name=Tokyo", n)
	}
}

func TestParseImport_MultipleURIs(t *testing.T) {
	text := "vless://a@h1:443#One\ntrojan://b@h2:443#Two\n"
	got, err := parseImport(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Kind != KindSubscription {
		t.Errorf("kind = %q, want %q", got.Kind, KindSubscription)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(got.Nodes))
	}
	if got.Nodes[0].SortOrder != 0 || got.Nodes[1].SortOrder != 1 {
		t.Errorf("sort orders not assigned in sequence: %+v", got.Nodes)
	}
}

func TestParseImport_Base64Subscription(t *testing.T) {
	inner := "vless://a@h1:443#One\nss://b@h2:8388#Two"
	encoded := base64.StdEncoding.EncodeToString([]byte(inner))
	got, err := parseImport(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 from decoded base64", len(got.Nodes))
	}
	if got.Nodes[1].Protocol != "ss" {
		t.Errorf("protocol = %q, want ss", got.Nodes[1].Protocol)
	}
}

func TestParseImport_Empty(t *testing.T) {
	if _, err := parseImport("   \n  "); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseImport_Garbage(t *testing.T) {
	_, err := parseImport("just some random words")
	if err == nil || !strings.Contains(err.Error(), "server links") {
		t.Fatalf("expected 'server links' error, got %v", err)
	}
}
