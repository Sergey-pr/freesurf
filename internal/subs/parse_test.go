package subs

import (
	"encoding/base64"
	"strings"
	"testing"

	"freesurf/internal/store"
)

func TestParseInline_SingleShareURI(t *testing.T) {
	got, err := parseInline("vless://uuid@host.example:443?type=tcp#Tokyo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Kind != store.KindManual {
		t.Errorf("kind = %q, want %q", got.Kind, store.KindManual)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
	if n := got.Nodes[0]; n.Protocol != "vless" || n.Name != "Tokyo" {
		t.Errorf("node = %+v, want protocol=vless name=Tokyo", n)
	}
}

func TestParseInline_MultipleURIs(t *testing.T) {
	got, err := parseInline("vless://a@h1:443#One\ntrojan://b@h2:443#Two\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Kind != store.KindSubscription {
		t.Errorf("kind = %q, want %q", got.Kind, store.KindSubscription)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(got.Nodes))
	}
	if got.Nodes[0].SortOrder != 0 || got.Nodes[1].SortOrder != 1 {
		t.Errorf("sort orders not assigned in sequence: %+v", got.Nodes)
	}
}

func TestParseInline_Base64(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("vless://a@h1:443#One\nss://b@h2:8388#Two"))
	got, err := parseInline(encoded)
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

func TestParseInline_Empty(t *testing.T) {
	if _, err := parseInline("   \n  "); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseInline_Garbage(t *testing.T) {
	_, err := parseInline("just some random words")
	if err == nil || !strings.Contains(err.Error(), "server links") {
		t.Fatalf("expected 'server links' error, got %v", err)
	}
}
