package main

import (
	"net"
	"net/url"
	"sync"
	"time"
)

const pingTimeout = 4 * time.Second

// pingNodeURI measures the TCP connect time (ms) to a node's server:port.
// Returns -1 if the host is unparseable or unreachable within pingTimeout.
// This is a reachability/RTT probe to the server, not a full proxy latency test.
func pingNodeURI(uri string) int {
	u, err := url.Parse(uri)
	if err != nil {
		return -1
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" {
		return -1
	}
	if port == "" {
		port = "443"
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), pingTimeout)
	if err != nil {
		return -1
	}
	_ = conn.Close()
	ms := int(time.Since(start).Milliseconds())
	if ms < 0 {
		ms = 0
	}
	return ms
}

// pingNodes pings many nodes concurrently, returning nodeID → latency ms (-1 = fail).
func pingNodes(nodes []Node) map[int64]int {
	res := make(map[int64]int, len(nodes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range nodes {
		n := nodes[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms := pingNodeURI(n.URI)
			mu.Lock()
			res[n.ID] = ms
			mu.Unlock()
		}()
	}
	wg.Wait()
	return res
}
