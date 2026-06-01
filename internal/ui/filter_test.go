package ui

import "testing"

func TestParseFilter(t *testing.T) {
	f, ignored := parseFilter("tag:Env=prod web-")
	if f.Tags["Env"] != "prod" {
		t.Errorf("want Env=prod, got %q", f.Tags["Env"])
	}
	if f.NamePrefix != "web-" {
		t.Errorf("want NamePrefix web-, got %q", f.NamePrefix)
	}
	if len(ignored) != 0 {
		t.Errorf("want no ignored tokens, got %v", ignored)
	}
}

func TestParseFilterNamePrefixToken(t *testing.T) {
	f, _ := parseFilter("name:db-")
	if f.NamePrefix != "db-" {
		t.Errorf("want db-, got %q", f.NamePrefix)
	}
	if f.Active() != true {
		t.Error("filter should be active")
	}
}

func TestParseFilterEmpty(t *testing.T) {
	if f, _ := parseFilter("   "); f.Active() {
		t.Error("blank filter should be inactive")
	}
}

func TestParseFilterRoundTrip(t *testing.T) {
	in := "tag:Env=prod web-"
	if f, _ := parseFilter(in); f.String() != in {
		t.Errorf("round trip: got %q want %q", f.String(), in)
	}
}

func TestParseFilterReportsIgnored(t *testing.T) {
	f, ignored := parseFilter("tag:Env web-") // missing =value
	if len(f.Tags) != 0 {
		t.Errorf("malformed tag should not set a constraint, got %v", f.Tags)
	}
	if len(ignored) != 1 || ignored[0] != "tag:Env" {
		t.Errorf("want [tag:Env] ignored, got %v", ignored)
	}
	if f.NamePrefix != "web-" {
		t.Errorf("valid bare token should still apply, got %q", f.NamePrefix)
	}
}

func TestParseFilterReportsShadowedNamePrefix(t *testing.T) {
	f, ignored := parseFilter("web- api-") // second bare token shadows the first
	if f.NamePrefix != "api-" {
		t.Errorf("last bare token should win, got %q", f.NamePrefix)
	}
	if len(ignored) != 1 || ignored[0] != "web-" {
		t.Errorf("want [web-] reported as shadowed, got %v", ignored)
	}
}
