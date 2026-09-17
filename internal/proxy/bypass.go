package proxy

import (
	"embed"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/idna"
)

// Rule-set files are fetched by cmd/fetchcores at build time; only the README is committed.
//
//go:embed rulesets
var ruleSetsFS embed.FS

// GeoRuleSets maps a supported geo tag to its embedded sing-box rule-set file.
var GeoRuleSets = map[string]string{
	"geoip:ru":            "geoip-ru.srs",
	"geosite:category-ru": "geosite-category-ru.srs",
	"geosite:reddit":      "geosite-reddit.srs",
}

// geoPrivate needs no rule-set: the base config already sends private ranges direct.
const geoPrivate = "geoip:private"

// maxBypassRules caps the list, since the privileged supervisor parses it.
const maxBypassRules = 1000

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Bypass lists what goes out directly instead of through the proxy.
type Bypass struct {
	CIDRs   []string `json:"cidrs,omitempty"`
	Domains []string `json:"domains,omitempty"`
	Geo     []string `json:"geo,omitempty"`
}

// ParseBypass reads one rule per line: an IP or CIDR, domain:example.com (or a bare
// domain, matching subdomains too), geoip:private or a tag from GeoRuleSets. # starts a comment.
func ParseBypass(text string) (Bypass, error) {
	var b Bypass
	for i, line := range strings.Split(text, "\n") {
		if c := strings.IndexByte(line, '#'); c >= 0 {
			line = line[:c]
		}
		rule := strings.ToLower(strings.TrimSpace(line))
		if rule == "" {
			continue
		}
		switch {
		case rule == geoPrivate:
		case strings.HasPrefix(rule, "geoip:") || strings.HasPrefix(rule, "geosite:"):
			if !slices.Contains(b.Geo, rule) {
				b.Geo = append(b.Geo, rule)
			}
		case net.ParseIP(rule) != nil:
			b.CIDRs = append(b.CIDRs, ipToCIDR(net.ParseIP(rule)))
		case strings.Contains(rule, "/"):
			b.CIDRs = append(b.CIDRs, rule)
		default:
			domain := strings.TrimPrefix(rule, "domain:")
			// Internationalized names such as рф are matched in their punycode form.
			if ascii, err := idna.Lookup.ToASCII(domain); err == nil {
				domain = ascii
			}
			b.Domains = append(b.Domains, domain)
		}
		if err := b.Validate(); err != nil {
			return Bypass{}, fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	return b, nil
}

// Validate rejects anything malformed; root builds its config from this.
func (b Bypass) Validate() error {
	if len(b.CIDRs)+len(b.Domains)+len(b.Geo) > maxBypassRules {
		return fmt.Errorf("too many bypass rules (max %d)", maxBypassRules)
	}
	for _, c := range b.CIDRs {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return fmt.Errorf("invalid IP or CIDR %q", c)
		}
	}
	for _, d := range b.Domains {
		if len(d) > 253 || !domainRe.MatchString(d) {
			return fmt.Errorf("invalid domain %q", d)
		}
	}
	for i, g := range b.Geo {
		if _, ok := GeoRuleSets[g]; !ok {
			return fmt.Errorf("unsupported geo tag %q (supported: %s)", g, supportedGeoTags())
		}
		// A repeated tag would be a duplicate rule-set, which sing-box rejects.
		if slices.Contains(b.Geo[:i], g) {
			return fmt.Errorf("duplicate geo tag %q", g)
		}
	}
	return nil
}

func supportedGeoTags() string {
	tags := append(slices.Collect(maps.Keys(GeoRuleSets)), geoPrivate)
	slices.Sort(tags)
	return strings.Join(tags, ", ")
}

// WriteRuleSets writes the embedded rule-set files that b references into dir.
// Nothing is touched without geo tags, so a build without fetched rule-sets still connects.
func WriteRuleSets(dir string, b Bypass) error {
	if len(b.Geo) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, g := range b.Geo {
		name := GeoRuleSets[g]
		data, err := ruleSetsFS.ReadFile("rulesets/" + name)
		if err != nil {
			return fmt.Errorf("rule-set %s is not embedded in this build (run `go generate ./internal/proxy` and rebuild): %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// bypassRules builds the direct route rules, the local DNS rule and the rule-set list.
func bypassRules(b Bypass, ruleSetDir string) (route []any, dns map[string]any, ruleSets []any) {
	var routeSets, dnsSets []any
	for _, g := range b.Geo {
		ruleSets = append(ruleSets, map[string]any{
			"type": "local", "tag": g, "format": "binary",
			"path": filepath.Join(ruleSetDir, GeoRuleSets[g]),
		})
		routeSets = append(routeSets, g)
		if strings.HasPrefix(g, "geosite:") {
			dnsSets = append(dnsSets, g)
		}
	}
	if len(b.CIDRs) > 0 {
		route = append(route, map[string]any{"ip_cidr": b.CIDRs, "outbound": "direct"})
	}
	if len(b.Domains) > 0 {
		route = append(route, map[string]any{"domain_suffix": b.Domains, "outbound": "direct"})
	}
	if len(routeSets) > 0 {
		route = append(route, map[string]any{"rule_set": routeSets, "outbound": "direct"})
	}
	// Bypassed domains resolve locally, so they get the addresses a direct connection should use.
	if len(b.Domains) > 0 || len(dnsSets) > 0 {
		dns = map[string]any{"server": "local-dns"}
		if len(b.Domains) > 0 {
			dns["domain_suffix"] = b.Domains
		}
		if len(dnsSets) > 0 {
			dns["rule_set"] = dnsSets
		}
	}
	return route, dns, ruleSets
}
