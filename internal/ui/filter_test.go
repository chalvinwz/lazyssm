package ui

import "testing"

func TestParseFilter(t *testing.T) {
	f := parseFilter("tag:Env=prod web-")
	if f.Tags["Env"] != "prod" {
		t.Errorf("want Env=prod, got %q", f.Tags["Env"])
	}
	if f.NamePrefix != "web-" {
		t.Errorf("want NamePrefix web-, got %q", f.NamePrefix)
	}
}

func TestParseFilterNamePrefixToken(t *testing.T) {
	f := parseFilter("name:db-")
	if f.NamePrefix != "db-" {
		t.Errorf("want db-, got %q", f.NamePrefix)
	}
	if f.Active() != true {
		t.Error("filter should be active")
	}
}

func TestParseFilterEmpty(t *testing.T) {
	if parseFilter("   ").Active() {
		t.Error("blank filter should be inactive")
	}
}

func TestParseFilterRoundTrip(t *testing.T) {
	in := "tag:Env=prod web-"
	if got := parseFilter(in).String(); got != in {
		t.Errorf("round trip: got %q want %q", got, in)
	}
}
