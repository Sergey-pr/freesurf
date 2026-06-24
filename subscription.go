package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Happ subscriptions gate on the official app's User-Agent and a per-device
// X-Hwid header; without them the server returns a "subscription.blocked"
// placeholder. We send the Happ UA and a stable per-install HWID.
const happUserAgent = "Happ/3.13.0"

// happHWID returns a stable per-install device id, generating and persisting one
// on first use so the provider counts this install as a single device.
func happHWID() string {
	if dir, err := appDataDir(); err == nil {
		p := filepath.Join(dir, "hwid")
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); len(id) >= 16 {
				return id
			}
		}
		if id := randomHex(16); id != "" {
			_ = os.WriteFile(p, []byte(id), 0600)
			return id
		}
	}
	return randomHex(16)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// fetchSubscription GETs a subscription URL with Happ headers and returns the raw body.
func fetchSubscription(ctx context.Context, subURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", happUserAgent)
	req.Header.Set("X-Hwid", happHWID())
	req.Header.Set("X-Device-Os", "Android")
	req.Header.Set("X-Device-Locale", "ru")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subscription server returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// nodesFromBody parses a subscription body (base64 blob or plain text) into nodes.
func nodesFromBody(body string) []Node {
	uris := collectURIs(nonEmptyLines(body))
	if len(uris) == 0 {
		if decoded, ok := tryBase64(body); ok {
			uris = collectURIs(nonEmptyLines(decoded))
		}
	}
	nodes := make([]Node, 0, len(uris))
	for i, uri := range uris {
		proto, name := describeURI(uri, i)
		nodes = append(nodes, Node{Name: name, URI: uri, Protocol: proto, SortOrder: i})
	}
	return nodes
}

// buildImport turns pasted text into a server + nodes, fetching over the network
// for Happ links and subscription URLs. Inline share URIs are handled offline.
func buildImport(ctx context.Context, text string) (*parsedImport, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrEmptyImport{}
	}

	if isHappLink(text) {
		subURL, err := decryptHapp(text)
		if err != nil {
			return nil, fmt.Errorf("could not decrypt Happ link: %w", err)
		}
		return importSubscription(ctx, subURL, "Happ subscription")
	}

	if lines := nonEmptyLines(text); len(lines) == 1 && isHTTPURL(lines[0]) {
		name := lines[0]
		if u, err := url.Parse(lines[0]); err == nil && u.Host != "" {
			name = u.Host
		}
		return importSubscription(ctx, lines[0], name)
	}

	// Inline content (one or more share URIs, possibly base64).
	return parseImport(text)
}

func importSubscription(ctx context.Context, subURL, name string) (*parsedImport, error) {
	body, err := fetchSubscription(ctx, subURL)
	if err != nil {
		return nil, err
	}

	nodes := nodesFromBody(body)
	if isBlockedPlaceholder(nodes) {
		return nil, fmt.Errorf("the subscription server rejected this client (\"app not supported\")")
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no servers found in the subscription")
	}

	u := subURL
	return &parsedImport{Kind: KindSubscription, Name: name, URL: &u, Nodes: nodes}, nil
}

// isBlockedPlaceholder detects the provider's "app not supported" sentinel, which
// is delivered as a single node pointing at the host "subscription.blocked".
func isBlockedPlaceholder(nodes []Node) bool {
	for _, n := range nodes {
		if strings.Contains(n.URI, "subscription.blocked") {
			return true
		}
	}
	return false
}
