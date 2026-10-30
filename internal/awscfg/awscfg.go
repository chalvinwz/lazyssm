// Package awscfg loads AWS configuration and constructs service clients.
package awscfg

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Clients bundles the AWS service clients lazyssm uses.
type Clients struct {
	Cfg aws.Config
	SSM *ssm.Client
	EC2 *ec2.Client
	STS *sts.Client
}

// Region returns the resolved AWS region, or "" if none is configured.
func (c Clients) Region() string { return c.Cfg.Region }

// Load resolves AWS configuration and builds the clients. A non-empty profile
// or region overrides AWS_PROFILE, AWS_REGION and the shared config; an empty
// one falls back to the default chain. SSO uses the standard SSO token cache.
func Load(ctx context.Context, profile, region string) (Clients, error) {
	var opts []func(*config.LoadOptions) error
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return Clients{}, err
	}
	return Clients{
		Cfg: cfg,
		SSM: ssm.NewFromConfig(cfg),
		EC2: ec2.NewFromConfig(cfg),
		STS: sts.NewFromConfig(cfg),
	}, nil
}
