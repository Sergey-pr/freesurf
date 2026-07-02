package ping

import (
	"fmt"
	"net"
	"net/url"
	"testing"
)

func TestQuicNode(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"alpn=h3", true},
		{"alpn=h3,h2", true},
		{"alpn=h2,h3", true},
		{"alpn=h2", false},
		{"security=tls", false},
		{"", false},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.query)
		if got := quicNode(q); got != c.want {
			t.Errorf("quicNode(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestURI_Reachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ln.Close()
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	uri := fmt.Sprintf("vless://uuid@%s:%s?security=reality&pbk=x", host, port)
	r := Probe(uri)
	if r.MS < 0 {
		t.Fatalf("Probe on live TCP server = %d (%v), want >= 0", r.MS, r.Err)
	}
	if r.Resolver != "literal" {
		t.Errorf("Resolver = %q, want literal", r.Resolver)
	}
}

func TestURI_Unreachable(t *testing.T) {
	// Bind then immediately release a local port so nothing listens on it: the
	// connect is refused deterministically instead of relying on network timeouts.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	host, port, _ := net.SplitHostPort(addr)

	uri := fmt.Sprintf("vless://uuid@%s:%s?security=none", host, port)
	r := Probe(uri)
	if r.MS != -1 {
		t.Errorf("Probe on closed port = %d, want -1", r.MS)
	}
	if r.Err == nil {
		t.Error("Probe on closed port should carry an error reason")
	}
}

func TestURI_BadInput(t *testing.T) {
	for _, uri := range []string{"", "://nope", "vless://uuid@:443"} {
		if ms := URI(uri); ms != -1 {
			t.Errorf("URI(%q) = %d, want -1", uri, ms)
		}
	}
}
