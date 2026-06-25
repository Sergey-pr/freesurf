// Package subs imports proxy servers from pasted text, subscription URLs, and
// encrypted Happ (happ://crypt5/) deep links.
package subs

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"freesurf/internal/store"
)

// ParsedImport is the result of interpreting an import: a server plus its nodes.
// The nodes' ServerID is filled in by the caller after the server is saved.
type ParsedImport struct {
	Kind  string
	Name  string
	URL   *string
	Nodes []store.Node
}

// ErrEmptyImport is returned when there is nothing usable to import.
type ErrEmptyImport struct{}

func (ErrEmptyImport) Error() string { return "clipboard is empty or contains no server" }

// parseInline interprets pasted text that already contains share URIs (or a
// base64 blob of them). Subscription/Happ URLs are handled in subscription.go.
func parseInline(text string) (*ParsedImport, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrEmptyImport{}
	}

	uris := collectURIs(nonEmptyLines(text))
	if len(uris) == 0 {
		if decoded, ok := tryBase64(text); ok {
			uris = collectURIs(nonEmptyLines(decoded))
		}
	}
	if len(uris) == 0 {
		return nil, fmt.Errorf("could not find any server links in the clipboard")
	}

	nodes := nodesFromURIs(uris)
	if len(nodes) == 1 {
		return &ParsedImport{Kind: store.KindManual, Name: nodes[0].Name, Nodes: nodes}, nil
	}
	return &ParsedImport{
		Kind:  store.KindSubscription,
		Name:  fmt.Sprintf("Imported (%d nodes)", len(nodes)),
		Nodes: nodes,
	}, nil
}

func nodesFromURIs(uris []string) []store.Node {
	nodes := make([]store.Node, 0, len(uris))
	for i, uri := range uris {
		proto, name := describeURI(uri, i)
		nodes = append(nodes, store.Node{Name: name, URI: uri, Protocol: proto, SortOrder: i})
	}
	return nodes
}

func nonEmptyLines(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(strings.TrimRight(raw, "\r")); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func collectURIs(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, "://") && !isHTTPURL(l) {
			out = append(out, l)
		}
	}
	return out
}

// tryBase64 decodes a base64 blob (std or url alphabet, padded or not) if it
// contains share URIs.
func tryBase64(s string) (string, bool) {
	compact := strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if data, err := enc.DecodeString(compact); err == nil && strings.Contains(string(data), "://") {
			return string(data), true
		}
	}
	return "", false
}

// describeURI extracts the protocol and a display name from a share URI. Some
// providers double-encode the #fragment name, so it is decoded up to twice.
func describeURI(uri string, index int) (protocol, name string) {
	protocol = "node"
	if i := strings.Index(uri, "://"); i > 0 {
		protocol = strings.ToLower(uri[:i])
	}
	if h := strings.LastIndex(uri, "#"); h >= 0 && h+1 < len(uri) {
		name = decodeFragment(uri[h+1:])
	}
	if name == "" {
		name = fmt.Sprintf("%s %d", protocol, index+1)
	}
	return protocol, name
}

func decodeFragment(s string) string {
	for i := 0; i < 2; i++ {
		dec, err := url.PathUnescape(s)
		if err != nil || dec == s {
			break
		}
		s = dec
	}
	return s
}
