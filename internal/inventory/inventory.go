// Package inventory fetches and merges AWS instance data from SSM and EC2.
package inventory

import (
	"context"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Source selects which instances the inventory surfaces.
type Source int

const (
	// SourceSSMOnly shows only SSM-managed (connectable) nodes. Default.
	SourceSSMOnly Source = iota
	// SourceAllEC2 shows all EC2 instances, marking non-SSM ones not-ready.
	SourceAllEC2
)

// Filter scopes a fetch via server-side tag and Name-prefix constraints.
type Filter struct {
	Tags       map[string]string
	NamePrefix string
}

// Active reports whether any constraint is set.
func (f Filter) Active() bool {
	return len(f.Tags) > 0 || f.NamePrefix != ""
}

// String renders the filter back into filter-bar syntax (tag:K=V … prefix).
func (f Filter) String() string {
	keys := sortedKeys(f.Tags)
	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		parts = append(parts, "tag:"+k+"="+f.Tags[k])
	}
	if f.NamePrefix != "" {
		parts = append(parts, f.NamePrefix)
	}
	return strings.Join(parts, " ")
}

// Sort orders instances pinned-first, then SSM-ready, then by Name/ID.
func Sort(in []Instance) { sortInstances(in) }

// Instance is a merged view of an EC2 instance and/or SSM managed node.
type Instance struct {
	InstanceID   string
	Name         string // EC2 Name tag
	PlatformType string
	IP           string
	AgentStatus  string // SSM agent status; "" when not an SSM node
	PingStatus   string // SSM ping status, e.g. "Online"
	State        string // EC2 instance state
	Tags         map[string]string
	SSMReady     bool // online in SSM -> connectable
	Pinned       bool
}

// ssmNode and ec2Inst are the minimal projections the merge operates on,
// keeping merge logic independent of the AWS SDK types for testability.
type ssmNode struct {
	ID           string
	PingStatus   string
	AgentStatus  string
	PlatformType string
	IP           string
}

type ec2Inst struct {
	ID    string
	Name  string
	IP    string
	State string
	Tags  map[string]string
}

// SSMAPI is the subset of the SSM client the inventory needs.
type SSMAPI interface {
	DescribeInstanceInformation(ctx context.Context, in *ssm.DescribeInstanceInformationInput, optFns ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
}

// EC2API is the subset of the EC2 client the inventory needs.
type EC2API interface {
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// sortedKeys returns a map's keys in stable sorted order, for deterministic
// rendering and request building.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildEC2Filters translates a Filter into EC2 DescribeInstances filters.
func buildEC2Filters(f Filter) []ec2types.Filter {
	// Deterministic order for stable requests/tests.
	keys := sortedKeys(f.Tags)
	out := make([]ec2types.Filter, 0, len(keys)+1)
	for _, k := range keys {
		out = append(out, ec2types.Filter{
			Name:   aws.String("tag:" + k),
			Values: []string{f.Tags[k]},
		})
	}
	if f.NamePrefix != "" {
		out = append(out, ec2types.Filter{
			Name:   aws.String("tag:Name"),
			Values: []string{f.NamePrefix + "*"},
		})
	}
	return out
}

// merge combines SSM nodes and EC2 instances by instance ID per the source mode.
func merge(ssmNodes []ssmNode, ec2Insts []ec2Inst, src Source, filterActive bool) []Instance {
	ssmByID := make(map[string]ssmNode, len(ssmNodes))
	for _, n := range ssmNodes {
		ssmByID[n.ID] = n
	}
	ec2ByID := make(map[string]ec2Inst, len(ec2Insts))
	for _, e := range ec2Insts {
		ec2ByID[e.ID] = e
	}

	capHint := len(ssmNodes)
	if src == SourceAllEC2 {
		capHint = len(ec2Insts)
	}
	out := make([]Instance, 0, capHint)
	switch src {
	case SourceAllEC2:
		for _, e := range ec2Insts {
			n, hasSSM := ssmByID[e.ID]
			out = append(out, mergeOne(e.ID, &e, ssmPtr(n, hasSSM)))
		}
	default: // SourceSSMOnly
		for _, n := range ssmNodes {
			e, hasEC2 := ec2ByID[n.ID]
			// Server-side filtering is EC2-based; when a filter is active a node
			// that is absent from the filtered EC2 set is considered filtered out.
			if filterActive && !hasEC2 {
				continue
			}
			out = append(out, mergeOne(n.ID, ec2Ptr(e, hasEC2), &n))
		}
	}
	sortInstances(out)
	return out
}

func ssmPtr(n ssmNode, ok bool) *ssmNode {
	if !ok {
		return nil
	}
	return &n
}

func ec2Ptr(e ec2Inst, ok bool) *ec2Inst {
	if !ok {
		return nil
	}
	return &e
}

func mergeOne(id string, e *ec2Inst, n *ssmNode) Instance {
	inst := Instance{InstanceID: id, Tags: map[string]string{}}
	if e != nil {
		inst.Name = e.Name
		inst.IP = e.IP
		inst.State = e.State
		if e.Tags != nil {
			inst.Tags = e.Tags
		}
	}
	if n != nil {
		inst.AgentStatus = n.AgentStatus
		inst.PingStatus = n.PingStatus
		inst.PlatformType = n.PlatformType
		if inst.IP == "" {
			inst.IP = n.IP
		}
		inst.SSMReady = strings.EqualFold(n.PingStatus, "Online")
	}
	return inst
}

// sortInstances orders pinned first, then SSM-ready, then by Name/ID.
func sortInstances(in []Instance) {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.SSMReady != b.SSMReady {
			return a.SSMReady
		}
		an, bn := a.Name, b.Name
		if an == "" {
			an = a.InstanceID
		}
		if bn == "" {
			bn = b.InstanceID
		}
		return naturalLess(an, bn)
	})
}

// naturalLess compares two strings so embedded number runs sort
// numerically: "g-2" < "g-10". Non-digit runs compare byte-wise.
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ai, bj := a[i], b[j]
		aDigit := ai >= '0' && ai <= '9'
		bDigit := bj >= '0' && bj <= '9'
		if aDigit && bDigit {
			is, js := i, j
			for i < len(a) && a[i] >= '0' && a[i] <= '9' {
				i++
			}
			for j < len(b) && b[j] >= '0' && b[j] <= '9' {
				j++
			}
			na := strings.TrimLeft(a[is:i], "0")
			nb := strings.TrimLeft(b[js:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			if (i - is) != (j - js) {
				return (i - is) < (j - js)
			}
			continue
		}
		if ai != bj {
			return ai < bj
		}
		i++
		j++
	}
	return len(a)-i < len(b)-j
}

// Fetch retrieves SSM nodes and EC2 instances, applies the server-side filter,
// and returns the merged inventory for the selected source.
func Fetch(ctx context.Context, ssmAPI SSMAPI, ec2API EC2API, f Filter, src Source) ([]Instance, error) {
	ssmNodes, err := fetchSSM(ctx, ssmAPI)
	if err != nil {
		return nil, err
	}
	ec2Insts, err := fetchEC2(ctx, ec2API, f)
	if err != nil {
		return nil, err
	}
	return merge(ssmNodes, ec2Insts, src, f.Active()), nil
}

func fetchSSM(ctx context.Context, api SSMAPI) ([]ssmNode, error) {
	var out []ssmNode
	var token *string
	for {
		resp, err := api.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
			NextToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, info := range resp.InstanceInformationList {
			out = append(out, ssmNode{
				ID:           aws.ToString(info.InstanceId),
				PingStatus:   string(info.PingStatus),
				AgentStatus:  agentStatus(info),
				PlatformType: string(info.PlatformType),
				IP:           aws.ToString(info.IPAddress),
			})
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}

func agentStatus(info ssmtypes.InstanceInformation) string {
	// Surface the agent version-based health as a coarse status string.
	if info.IsLatestVersion != nil && *info.IsLatestVersion {
		return "Latest"
	}
	return aws.ToString(info.AgentVersion)
}

func fetchEC2(ctx context.Context, api EC2API, f Filter) ([]ec2Inst, error) {
	var out []ec2Inst
	var token *string
	filters := buildEC2Filters(f)
	for {
		in := &ec2.DescribeInstancesInput{NextToken: token}
		if len(filters) > 0 {
			in.Filters = filters
		}
		resp, err := api.DescribeInstances(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, r := range resp.Reservations {
			for _, inst := range r.Instances {
				out = append(out, fromEC2(inst))
			}
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		token = resp.NextToken
	}
	return out, nil
}

func fromEC2(inst ec2types.Instance) ec2Inst {
	tags := make(map[string]string, len(inst.Tags))
	for _, t := range inst.Tags {
		tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	ip := aws.ToString(inst.PrivateIpAddress)
	if ip == "" {
		ip = aws.ToString(inst.PublicIpAddress)
	}
	var state string
	if inst.State != nil {
		state = string(inst.State.Name)
	}
	return ec2Inst{
		ID:    aws.ToString(inst.InstanceId),
		Name:  tags["Name"],
		IP:    ip,
		State: state,
		Tags:  tags,
	}
}
