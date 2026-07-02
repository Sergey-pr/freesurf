// Package ping measures TCP-connect latency to proxy node servers.
package ping

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"freesurf/internal/dnsresolve"
)

const (
	timeout        = 5 * time.Second // TCP connect
	maxConcurrency = 16              // nodes probed at once in AllDetailed
)

// Result is one probe outcome, carrying enough detail to log why it failed.
type Result struct {
	MS       int    // latency in ms, or -1 on failure
	Host     string // host:port from the URI
	Method   string // "tcp" or "quic" (transport probed)
	Resolver string // how Host was resolved (see dnsresolve.Host)
	IP       string // IP connected to, or last tried on failure
	Err      error  // failure reason, nil on success
}

// Log renders the result as a single human-readable diagnostic line.
func (r Result) Log() string {
	via := r.Resolver
	if r.IP != "" {
		via += " " + r.IP
	}
	if r.MS < 0 {
		reason := "unreachable"
		if r.Err != nil {
			reason = r.Err.Error()
		}
		return fmt.Sprintf("%s [%s] via %s -> FAIL: %s", r.Host, r.Method, via, reason)
	}
	return fmt.Sprintf("%s [%s] via %s -> %d ms", r.Host, r.Method, via, r.MS)
}

// Probe measures TCP-connect latency to the server in a vless:// (or similar) share
// URI, returning a Result with the outcome and (on failure) the reason.
//
// The host is resolved via dnsresolve (DoH + system, unioned), so a censored or
// poisoned local DNS path doesn't produce false timeouts - the tunnel resolves the
// same way. Every resolved IP is probed in parallel and the first to answer wins.
//
// The transport is chosen from the URI: nodes advertising HTTP/3 (alpn=h3) run
// over QUIC/UDP, so a TCP connect would falsely time out - they are probed with a
// QUIC packet instead. Every other node gets a plain TCP connect. TCP nodes are not
// TLS-handshaked, because Reality servers reset unauthenticated handshakes and a
// bare connect is what tells reachable from blocked. It measures reachability to
// the server, not full proxy latency.
func Probe(uri string) Result {
	u, err := url.Parse(uri)
	if err != nil {
		return Result{MS: -1, Err: fmt.Errorf("bad uri: %w", err)}
	}
	host := u.Hostname()
	if host == "" {
		return Result{MS: -1, Err: errors.New("uri has no host")}
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}

	quic := quicNode(u.Query())
	r := Result{Host: net.JoinHostPort(host, port), Method: "tcp"}
	if quic {
		r.Method = "quic"
	}

	ips, resolver := dnsresolve.Host(host)
	r.Resolver = resolver
	if len(ips) == 0 {
		r.MS = -1
		r.Err = fmt.Errorf("resolve %s: no addresses", host)
		return r
	}

	ms, ip, perr := probeIPs(ips, port, quic)
	r.IP = ip
	if perr != nil {
		r.MS = -1
		r.Err = perr
		return r
	}
	r.MS = ms
	return r
}

// quicNode reports whether the node's transport runs over QUIC/UDP, signalled by
// an h3 entry in the alpn list (HTTP/3).
func quicNode(q url.Values) bool {
	for _, a := range strings.Split(q.Get("alpn"), ",") {
		if strings.TrimSpace(a) == "h3" {
			return true
		}
	}
	return false
}

// URI returns just the latency (ms) from Probe, or -1 on failure.
func URI(uri string) int { return Probe(uri).MS }

// probeIPs dials every IP in parallel (TCP, or QUIC/UDP when quic is set) and
// returns the first that answers (its latency and address). If all fail it returns
// -1 with the last error and IP.
func probeIPs(ips []string, port string, quic bool) (int, string, error) {
	type outcome struct {
		ms  int
		ip  string
		err error
	}
	results := make(chan outcome, len(ips))
	for _, ip := range ips {
		go func(ip string) {
			addr := net.JoinHostPort(ip, port)
			start := time.Now()
			var err error
			if quic {
				err = quicProbe(addr)
			} else {
				err = tcpConnect(addr)
			}
			ms := -1
			if err == nil {
				ms = int(time.Since(start).Milliseconds())
			}
			results <- outcome{ms, ip, err}
		}(ip)
	}
	var last outcome
	for range ips {
		o := <-results
		if o.err == nil {
			if o.ms < 0 {
				o.ms = 0
			}
			return o.ms, o.ip, nil
		}
		last = o
	}
	return -1, last.ip, last.err
}

func tcpConnect(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// quicProbe checks UDP reachability of a QUIC server by sending an Initial packet
// with a reserved, unsupported version, which forces the server to reply with a
// Version Negotiation packet. Any reply proves the QUIC endpoint is reachable - no
// TLS/crypto needed. A TCP connect would falsely time out on these UDP-only nodes.
func quicProbe(addr string) error {
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(quicVersionNegotiationTrigger()); err != nil {
		return err
	}
	buf := make([]byte, 1500)
	_, err = conn.Read(buf) // any reply = reachable
	return err
}

// quicVersionNegotiationTrigger builds a QUIC long-header Initial packet using the
// reserved version 0x1a1a1a1a. Per RFC 9000 a server that doesn't support the
// version must answer with a Version Negotiation packet. The packet is padded to
// the 1200-byte minimum required for Initial packets.
func quicVersionNegotiationTrigger() []byte {
	pkt := make([]byte, 1200)
	pkt[0] = 0xC0 // long header form + fixed bit
	pkt[1], pkt[2], pkt[3], pkt[4] = 0x1a, 0x1a, 0x1a, 0x1a
	pkt[5] = 8 // DCID length
	_, _ = rand.Read(pkt[6:14])
	pkt[14] = 8 // SCID length
	_, _ = rand.Read(pkt[15:23])
	return pkt
}

// AllDetailed probes many URIs concurrently, returning id → Result. At most
// maxConcurrency nodes are probed at once so a large subscription doesn't flood the
// network with a wall of simultaneous connects (each node itself still probes its
// DNS lookups and resolved IPs in parallel).
func AllDetailed(uris map[int64]string) map[int64]Result {
	res := make(map[int64]Result, len(uris))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)
	for id, uri := range uris {
		wg.Add(1)
		go func(id int64, uri string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := Probe(uri)
			mu.Lock()
			res[id] = r
			mu.Unlock()
		}(id, uri)
	}
	wg.Wait()
	return res
}
