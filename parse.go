package main

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// parsedImport is the best-effort result of interpreting pasted clipboard text.
//
// NOTE: this is a deliberately shallow parser for the v1 scaffold. It only
// recognises the *shape* of the input (a subscription URL vs. one-or-more share
// URIs) so the UI has something to display. Real subscription fetching and full
// protocol decoding (VLESS/VMess/Trojan/SS/Hysteria2/TUIC/WG…) belong to the
// sing-box engine layer added in a later milestone.
type parsedImport struct {
	Kind  string
	Name  string
	URL   *string
	Nodes []Node // ServerID is filled in by the caller after the server is saved
}

// ErrEmptyImport is returned when the clipboard holds nothing usable.
type ErrEmptyImport struct{}

func (ErrEmptyImport) Error() string { return "clipboard is empty or contains no server" }

// parseImport interprets pasted text into a server + its nodes.
func parseImport(text string) (*parsedImport, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrEmptyImport{}
	}

	lines := nonEmptyLines(text)

	// Single subscription URL → a subscription source with no nodes yet.
	if len(lines) == 1 && isHTTPURL(lines[0]) {
		u := lines[0]
		name := u
		if parsed, err := url.Parse(u); err == nil && parsed.Host != "" {
			name = parsed.Host
		}
		return &parsedImport{
			Kind:  KindSubscription,
			Name:  name,
			URL:   &u,
			Nodes: []Node{},
		}, nil
	}

	// Otherwise, collect share URIs. If none are present inline, the payload may
	// be base64-encoded subscription content; try to decode it first.
	uris := collectURIs(lines)
	if len(uris) == 0 {
		if decoded, ok := tryBase64(text); ok {
			uris = collectURIs(nonEmptyLines(decoded))
		}
	}
	if len(uris) == 0 {
		return nil, fmt.Errorf("could not find any server links in the clipboard")
	}

	nodes := make([]Node, 0, len(uris))
	for i, uri := range uris {
		proto, name := describeURI(uri, i)
		nodes = append(nodes, Node{
			Name:      name,
			URI:       uri,
			Protocol:  proto,
			SortOrder: i,
		})
	}

	if len(nodes) == 1 {
		return &parsedImport{Kind: KindManual, Name: nodes[0].Name, Nodes: nodes}, nil
	}
	return &parsedImport{
		Kind:  KindSubscription,
		Name:  fmt.Sprintf("Imported (%d nodes)", len(nodes)),
		Nodes: nodes,
	}, nil
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

// tryBase64 attempts to decode a base64 blob (std or url alphabet, padded or not).
func tryBase64(s string) (string, bool) {
	compact := strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if data, err := enc.DecodeString(compact); err == nil && strings.Contains(string(data), "://") {
			return string(data), true
		}
	}
	return "", false
}

// describeURI extracts the protocol and a display name from a share URI.
func describeURI(uri string, index int) (protocol, name string) {
	protocol = "node"
	if i := strings.Index(uri, "://"); i > 0 {
		protocol = strings.ToLower(uri[:i])
	}
	if parsed, err := url.Parse(uri); err == nil && parsed.Fragment != "" {
		name = parsed.Fragment
	}
	if name == "" {
		name = fmt.Sprintf("%s %d", protocol, index+1)
	}
	return protocol, name
}
