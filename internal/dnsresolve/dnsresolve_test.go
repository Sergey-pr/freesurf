package dnsresolve

import "testing"

func TestHostLiteral(t *testing.T) {
	ips, resolver := Host("203.0.113.7")
	if resolver != "literal" {
		t.Errorf("resolver = %q, want literal", resolver)
	}
	if len(ips) != 1 || ips[0] != "203.0.113.7" {
		t.Errorf("ips = %v, want [203.0.113.7]", ips)
	}
	if got := FirstIP("203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("FirstIP = %q, want 203.0.113.7", got)
	}
}

func TestHostEmpty(t *testing.T) {
	if ips, resolver := Host(""); len(ips) != 0 || resolver != "none" {
		t.Errorf("Host(\"\") = %v, %q; want nil, none", ips, resolver)
	}
	if got := FirstIP(""); got != "" {
		t.Errorf("FirstIP(\"\") = %q, want empty", got)
	}
}
