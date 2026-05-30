package ui

import (
	"testing"

	"charm.land/bubbles/v2/textinput"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

func TestRecomputeFiltersByName(t *testing.T) {
	// Instance IDs contain the digit 7; the search must NOT match rows via the
	// ID (regression: "group7" previously matched every row through hex IDs).
	all := []inventory.Instance{
		{Name: "Group7-app-public", InstanceID: "i-07a1"},
		{Name: "Group7-app-private", InstanceID: "i-0b72"},
		{Name: "Group1-url-shortener", InstanceID: "i-0777aaa"}, // ID full of 7s
		{Name: "Group10-prod", InstanceID: "i-07c9"},
		{Name: "Group8-twitter", InstanceID: "i-0e7f"},
	}

	si := textinput.New()
	si.SetValue("group7")
	m := &Model{all: all, searchInput: si, mode: modeSearch}
	m.recompute()

	if len(m.visible) != 2 {
		t.Fatalf("want 2 matches for group7, got %d: %v", len(m.visible), names(m.visible))
	}
	for _, in := range m.visible {
		if in.Name[:6] != "Group7" {
			t.Errorf("unexpected match: %q", in.Name)
		}
	}
}

func TestRecomputeEmptyShowsAll(t *testing.T) {
	all := []inventory.Instance{{Name: "a"}, {Name: "b"}}
	si := textinput.New()
	m := &Model{all: all, searchInput: si}
	m.recompute()
	if len(m.visible) != 2 {
		t.Fatalf("empty query should show all, got %d", len(m.visible))
	}
}

func names(in []inventory.Instance) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[i].Name
	}
	return out
}
