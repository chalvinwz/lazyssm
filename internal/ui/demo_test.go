package ui

import (
	"strings"
	"testing"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

func TestNewDemo(t *testing.T) {
	m := NewDemo("", "")
	if !m.demo || !m.binariesOK {
		t.Fatalf("demo model should set demo+binariesOK, got demo=%v binariesOK=%v", m.demo, m.binariesOK)
	}
	if m.clients.Region() == "" {
		t.Error("demo model should default a region for the title bar")
	}
	if m.Init() == nil {
		t.Error("demo Init should return a fetch cmd")
	}
}

func TestFilterDemoNarrows(t *testing.T) {
	all := demoInstances()

	// SSM-only hides the one non-SSM node (no ping status).
	ssm := filterDemo(all, inventory.Filter{}, inventory.SourceSSMOnly)
	if len(ssm) != len(all)-1 {
		t.Fatalf("SSM-only should drop 1 non-SSM node, got %d of %d", len(ssm), len(all))
	}

	// All-EC2 keeps everything.
	if got := filterDemo(all, inventory.Filter{}, inventory.SourceAllEC2); len(got) != len(all) {
		t.Errorf("AllEC2 should keep all %d, got %d", len(all), len(got))
	}

	// Tag filter scopes by exact tag.
	prod := filterDemo(all, inventory.Filter{Tags: map[string]string{"Env": "prod"}}, inventory.SourceAllEC2)
	if len(prod) == 0 {
		t.Fatal("expected some Env=prod instances")
	}
	for _, it := range prod {
		if it.Tags["Env"] != "prod" {
			t.Errorf("%s leaked into Env=prod filter", it.Name)
		}
	}

	// Name prefix scopes by Name.
	web := filterDemo(all, inventory.Filter{NamePrefix: "web-"}, inventory.SourceAllEC2)
	if len(web) == 0 {
		t.Fatal("expected some web- instances")
	}
	for _, it := range web {
		if !strings.HasPrefix(it.Name, "web-") {
			t.Errorf("%s leaked into web- prefix filter", it.Name)
		}
	}
}
