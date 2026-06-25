// Package ping measures TCP-connect latency to proxy node servers.
package ping

import (
	"net"
	"net/url"
	"sync"
	"time"
)

const timeout = 4 * time.Second

// URI returns the TCP connect time (ms) to the server in a vless:// (or similar)
// share URI, or -1 if the host is unparseable or unreachable within the timeout.
// It is a reachability/RTT probe to the server, not a full proxy latency test.
func URI(uri string) int {
	u, err := url.Parse(uri)
	if err != nil {
		return -1
	}
	host := u.Hostname()
	if host == "" {
		return -1
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return -1
	}
	_ = conn.Close()
	if ms := int(time.Since(start).Milliseconds()); ms > 0 {
		return ms
	}
	return 0
}

// All probes many URIs concurrently, returning id → latency ms (-1 = fail).
func All(uris map[int64]string) map[int64]int {
	res := make(map[int64]int, len(uris))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id, uri := range uris {
		wg.Add(1)
		go func(id int64, uri string) {
			defer wg.Done()
			ms := URI(uri)
			mu.Lock()
			res[id] = ms
			mu.Unlock()
		}(id, uri)
	}
	wg.Wait()
	return res
}
