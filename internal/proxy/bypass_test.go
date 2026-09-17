package proxy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseBypass(t *testing.T) {
	got, err := ParseBypass("# comment\n  GeoIP:RU \ngeosite:category-ru\ngeoip:ru\n\n1.2.3.4\n10.0.0.0/8 # lan\n2001:db8::/32\ndomain:Example.com\nsub.yandex.ru\r\n")
	if err != nil {
		t.Fatal(err)
	}
	want := Bypass{
		CIDRs:   []string{"1.2.3.4/32", "10.0.0.0/8", "2001:db8::/32"},
		Domains: []string{"example.com", "sub.yandex.ru"},
		Geo:     []string{"geoip:ru", "geosite:category-ru"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseBypassRussianZones(t *testing.T) {
	got, err := ParseBypass("geoip:private\ndomain:ru\ndomain:su\ndomain:рф\nпример.рф")
	if err != nil {
		t.Fatal(err)
	}
	want := Bypass{Domains: []string{"ru", "su", "xn--p1ai", "xn--e1afmkfd.xn--p1ai"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestParseBypassRejects(t *testing.T) {
	for _, text := range []string{
		"geoip:us",
		"geosite:google",
		"10.0.0.0/33",
		"bad_domain.com",
		"-example.com",
		`evil.com","x":"`,
		"example..com",
		"ok.com\n1.2.3/8",
	} {
		if _, err := ParseBypass(text); err == nil {
			t.Errorf("%q was accepted", text)
		}
	}
	_, err := ParseBypass("ok.com\n1.2.3/8")
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %v does not name line 2", err)
	}
}

func TestValidateCatchesTamperedRequests(t *testing.T) {
	for _, b := range []Bypass{
		{Geo: []string{"geoip:ru", "geoip:ru"}},
		{Geo: []string{"../../etc/passwd"}},
		{Domains: []string{"a b"}},
		{CIDRs: []string{"1.2.3.4"}},
		{Domains: make([]string, maxBypassRules+1)},
	} {
		if b.Validate() == nil {
			t.Errorf("%#v passed validation", b)
		}
	}
}

func TestSingboxConfigBypass(t *testing.T) {
	bypass := Bypass{CIDRs: []string{"10.0.0.0/8"}, Domains: []string{"example.com"}, Geo: []string{"geoip:ru", "geosite:category-ru"}}
	data, err := SingboxConfig("198.51.100.7", bypass, "/rules")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		DNS struct {
			Rules []map[string]any `json:"rules"`
		} `json:"dns"`
		Route struct {
			Rules   []map[string]any `json:"rules"`
			RuleSet []map[string]any `json:"rule_set"`
		} `json:"route"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Route.RuleSet) != 2 {
		t.Fatalf("rule_set = %v, want both geo tags", doc.Route.RuleSet)
	}
	bypassRoute := doc.Route.Rules[len(doc.Route.Rules)-3:]
	for i, key := range []string{"ip_cidr", "domain_suffix", "rule_set"} {
		if bypassRoute[i][key] == nil || bypassRoute[i]["outbound"] != "direct" {
			t.Errorf("bypass rule %d = %v, want %s to direct", i, bypassRoute[i], key)
		}
	}
	first := doc.DNS.Rules[0]
	if first["server"] != "local-dns" || first["rule_set"] == nil || first["domain_suffix"] == nil {
		t.Errorf("first DNS rule = %v, want bypassed domains on local-dns", first)
	}
	sets := first["rule_set"].([]any)
	if len(sets) != 1 || sets[0] != "geosite:category-ru" {
		t.Errorf("DNS rule_set = %v, want only the geosite tag", sets)
	}
}
