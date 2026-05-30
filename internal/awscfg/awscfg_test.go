package awscfg

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestRegionAccessor(t *testing.T) {
	c := Clients{Cfg: aws.Config{Region: "ap-southeast-1"}}
	if c.Region() != "ap-southeast-1" {
		t.Errorf("Region() = %q, want ap-southeast-1", c.Region())
	}
}

func TestRegionAccessorEmpty(t *testing.T) {
	if got := (Clients{}).Region(); got != "" {
		t.Errorf("zero-value Clients region = %q, want empty", got)
	}
}

// TestLoadAppliesRegion verifies the explicit --region argument wins end to end.
// LoadDefaultConfig is lazy (credentials resolve on first API call, not here),
// so this is safe in CI with no AWS credentials configured.
func TestLoadAppliesRegion(t *testing.T) {
	c, err := Load(context.Background(), "", "us-west-2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Region() != "us-west-2" {
		t.Errorf("region precedence: got %q, want us-west-2", c.Region())
	}
	if c.SSM == nil || c.EC2 == nil || c.STS == nil {
		t.Error("clients must be constructed (SSM/EC2/STS non-nil)")
	}
}
