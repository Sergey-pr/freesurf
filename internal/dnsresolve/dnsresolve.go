// Package dnsresolve resolves hostnames using encrypted DoH plus the system
// resolver in parallel, unioning the results. A censored or poisoned local DNS
// path can return a blackholed address (or nothing) for VPN server domains; DoH
// bypasses it, so callers see the real IPs. All results are IPv4 to match the
// tunnel's prefer_ipv4 strategy.
package dnsresolve

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const dnsTimeout = 3 * time.Second

// Host resolves host to IPv4 addresses via DoH and the system resolver in
// parallel, unioning the results. resolver names which sources contributed:
// "literal", "doh", "system", "doh+system", or "none".
func Host(host string) (ips []string, resolver string) {
	if host == "" {
		return nil, "none"
	}
	if net.ParseIP(host) != nil {
		return []string{host}, "literal"
	}

	type res struct {
		name string
		ips  []string
	}
	ch := make(chan res, 2)
	go func() { ch <- res{"doh", dohResolve(host)} }()
	go func() { ch <- res{"system", systemResolve(host)} }()

	seen := map[string]bool{}
	var srcs []string
	for i := 0; i < 2; i++ {
		r := <-ch
		if len(r.ips) > 0 {
			srcs = append(srcs, r.name)
		}
		for _, ip := range r.ips {
			if !seen[ip] {
				seen[ip] = true
				ips = append(ips, ip)
			}
		}
	}
	sort.Strings(ips)
	sort.Strings(srcs)
	if len(srcs) == 0 {
		return nil, "none"
	}
	return ips, strings.Join(srcs, "+")
}

// FirstIP returns one resolved IPv4 for host, or "" if it can't be resolved. The
// result is deterministic (Host's addresses are sorted), which lets callers pin a
// single agreed-upon server IP.
func FirstIP(host string) string {
	ips, _ := Host(host)
	if len(ips) == 0 {
		return ""
	}
	return ips[0]
}

// dohResolve queries Cloudflare's DoH JSON endpoint over HTTPS to 1.1.1.1 for A
// records, returning nil on any error so the caller can fall back.
func dohResolve(host string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://1.1.1.1/dns-query?type=A&name="+url.QueryEscape(host), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("accept", "application/dns-json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	var body struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil
	}
	var ips []string
	for _, a := range body.Answer {
		if a.Type == 1 && net.ParseIP(a.Data) != nil { // 1 = A record
			ips = append(ips, a.Data)
		}
	}
	return ips
}

// systemResolve resolves host via the OS resolver, returning IPv4 addresses.
func systemResolve(host string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	var ips []string
	for _, a := range addrs {
		if a.IP.To4() != nil {
			ips = append(ips, a.IP.String())
		}
	}
	return ips
}
