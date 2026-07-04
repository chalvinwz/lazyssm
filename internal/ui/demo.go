package ui

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/inventory"
	"github.com/chalvinwz/lazyssm/internal/store"
)

// NewDemo builds a Model preloaded with fixed sample data and no AWS dependency,
// for recording screenshots/GIFs via LAZYSSM_DEMO=1. It never calls AWS: fetch,
// preflight, and connect are all short-circuited.
func NewDemo(profile, region string) Model {
	if region == "" {
		region = "ap-southeast-3"
	}
	if profile == "" {
		profile = "demo"
	}
	st, _ := store.LoadFrom("") // in-memory; no disk writes
	m := New(awscfg.Clients{Cfg: aws.Config{Region: region}}, st, profile, region, false)
	m.demo = true
	m.binariesOK = true // skip the preflight modal in demo mode
	return m
}

// demoInstances returns a fixed, representative inventory for demo recordings.
func demoInstances() []inventory.Instance {
	return []inventory.Instance{
		{InstanceID: "i-07a1b2c3d4e5f6a7b", Name: "web-prod-1", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.4.12", PlatformType: "Linux", AgentStatus: "Latest", Tags: map[string]string{"Env": "prod", "Team": "platform"}},
		{InstanceID: "i-0b72aa01cc93de45f", Name: "web-prod-2", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.4.13", PlatformType: "Linux", AgentStatus: "Latest", Tags: map[string]string{"Env": "prod", "Team": "platform"}},
		{InstanceID: "i-0e7f1234aa5678bcd", Name: "api-prod-1", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.5.20", PlatformType: "Linux", AgentStatus: "Latest", Tags: map[string]string{"Env": "prod", "Team": "api"}},
		{InstanceID: "i-0777aaaabbbb1234c", Name: "worker-prod-1", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.1.9", PlatformType: "Linux", AgentStatus: "Latest", Tags: map[string]string{"Env": "prod", "Team": "data"}},
		{InstanceID: "i-0ff00ee11dd22cc33", Name: "win-build-1", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.9.31", PlatformType: "Windows", AgentStatus: "Latest", Tags: map[string]string{"Env": "ci", "Team": "platform"}},
		{InstanceID: "i-0c9aa55dd66ee7788", Name: "db-staging-1", SSMReady: false, PingStatus: "ConnectionLost", State: "stopped", IP: "10.0.8.4", PlatformType: "Linux", Tags: map[string]string{"Env": "staging", "Team": "data"}},
		{InstanceID: "i-0aa11bb22cc33dd44", Name: "bastion-legacy", SSMReady: false, State: "running", IP: "10.0.0.7", Tags: map[string]string{"Env": "prod"}},
	}
}

// filterDemo mimics server-side scoping over the sample set so the demo's filter
// (f) and source toggle (t) actually narrow the list during a recording.
func filterDemo(in []inventory.Instance, f inventory.Filter, src inventory.Source) []inventory.Instance {
	out := make([]inventory.Instance, 0, len(in))
	for _, it := range in {
		// SSM-only hides nodes that aren't SSM-managed (no ping status).
		if src == inventory.SourceSSMOnly && it.PingStatus == "" {
			continue
		}
		if !matchDemoFilter(it, f) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func matchDemoFilter(it inventory.Instance, f inventory.Filter) bool {
	for k, v := range f.Tags {
		if it.Tags[k] != v {
			return false
		}
	}
	if f.NamePrefix != "" && !strings.HasPrefix(it.Name, f.NamePrefix) {
		return false
	}
	return true
}
