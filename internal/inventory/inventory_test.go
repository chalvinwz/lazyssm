package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestBuildEC2Filters(t *testing.T) {
	f := Filter{
		Tags:       map[string]string{"Env": "prod", "App": "web"},
		NamePrefix: "web-",
	}
	got := buildEC2Filters(f)
	// Tags sorted (App, Env) then Name prefix.
	if len(got) != 3 {
		t.Fatalf("want 3 filters, got %d", len(got))
	}
	if *got[0].Name != "tag:App" || got[0].Values[0] != "web" {
		t.Errorf("filter[0] = %v=%v", *got[0].Name, got[0].Values)
	}
	if *got[1].Name != "tag:Env" || got[1].Values[0] != "prod" {
		t.Errorf("filter[1] = %v=%v", *got[1].Name, got[1].Values)
	}
	if *got[2].Name != "tag:Name" || got[2].Values[0] != "web-*" {
		t.Errorf("filter[2] = %v=%v", *got[2].Name, got[2].Values)
	}
}

func TestBuildEC2FiltersEmpty(t *testing.T) {
	if got := buildEC2Filters(Filter{}); len(got) != 0 {
		t.Fatalf("want no filters, got %d", len(got))
	}
}

func TestMergeBoth(t *testing.T) {
	ssmNodes := []ssmNode{{ID: "i-1", PingStatus: "Online", PlatformType: "Linux", IP: "10.0.0.1"}}
	ec2Insts := []ec2Inst{{ID: "i-1", Name: "web-1", IP: "10.0.0.1", State: "running", Tags: map[string]string{"Name": "web-1", "Env": "prod"}}}

	got := merge(ssmNodes, ec2Insts, SourceSSMOnly, false)
	if len(got) != 1 {
		t.Fatalf("want 1 instance, got %d", len(got))
	}
	in := got[0]
	if !in.SSMReady {
		t.Error("want SSMReady true for Online node")
	}
	if in.Name != "web-1" {
		t.Errorf("want Name web-1, got %q", in.Name)
	}
	if in.Tags["Env"] != "prod" {
		t.Errorf("want Env tag prod, got %q", in.Tags["Env"])
	}
}

func TestMergeSSMOnlyHybridNoEC2(t *testing.T) {
	// Hybrid/on-prem node present in SSM but not EC2; included when no filter.
	ssmNodes := []ssmNode{{ID: "mi-1", PingStatus: "Online", PlatformType: "Linux"}}
	got := merge(ssmNodes, nil, SourceSSMOnly, false)
	if len(got) != 1 || got[0].InstanceID != "mi-1" {
		t.Fatalf("want hybrid node included, got %+v", got)
	}
	if !got[0].SSMReady {
		t.Error("want SSMReady true")
	}
}

func TestMergeSSMOnlyFilterDropsNonEC2(t *testing.T) {
	// With an active filter, an SSM node missing from the filtered EC2 set is dropped.
	ssmNodes := []ssmNode{
		{ID: "i-1", PingStatus: "Online"},
		{ID: "i-2", PingStatus: "Online"},
	}
	ec2Insts := []ec2Inst{{ID: "i-1", Name: "web-1"}}
	got := merge(ssmNodes, ec2Insts, SourceSSMOnly, true)
	if len(got) != 1 || got[0].InstanceID != "i-1" {
		t.Fatalf("want only i-1 retained, got %+v", got)
	}
}

func TestMergeAllEC2MarksNotReady(t *testing.T) {
	ssmNodes := []ssmNode{{ID: "i-1", PingStatus: "Online"}}
	ec2Insts := []ec2Inst{
		{ID: "i-1", Name: "web-1"},
		{ID: "i-2", Name: "db-1"}, // no SSM agent
	}
	got := merge(ssmNodes, ec2Insts, SourceAllEC2, false)
	if len(got) != 2 {
		t.Fatalf("want 2 instances, got %d", len(got))
	}
	ready := map[string]bool{}
	for _, in := range got {
		ready[in.InstanceID] = in.SSMReady
	}
	if !ready["i-1"] {
		t.Error("i-1 should be SSM-ready")
	}
	if ready["i-2"] {
		t.Error("i-2 should NOT be SSM-ready")
	}
}

func TestMergeOfflinePingNotReady(t *testing.T) {
	ssmNodes := []ssmNode{{ID: "i-1", PingStatus: "ConnectionLost"}}
	ec2Insts := []ec2Inst{{ID: "i-1", Name: "web-1"}}
	got := merge(ssmNodes, ec2Insts, SourceSSMOnly, false)
	if got[0].SSMReady {
		t.Error("ConnectionLost node must not be SSM-ready")
	}
}

// --- fakes for Fetch ---

type fakeSSM struct {
	pages []*ssm.DescribeInstanceInformationOutput
	calls int
}

func (f *fakeSSM) DescribeInstanceInformation(_ context.Context, _ *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	out := f.pages[f.calls]
	f.calls++
	return out, nil
}

type fakeEC2 struct {
	out      *ec2.DescribeInstancesOutput
	gotInput *ec2.DescribeInstancesInput
}

func (f *fakeEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.gotInput = in
	return f.out, nil
}

func TestFetchPaginatesSSMAndPassesFilters(t *testing.T) {
	sfake := &fakeSSM{pages: []*ssm.DescribeInstanceInformationOutput{
		{
			InstanceInformationList: []ssmtypes.InstanceInformation{
				{InstanceId: aws.String("i-1"), PingStatus: ssmtypes.PingStatusOnline},
			},
			NextToken: aws.String("page2"),
		},
		{
			InstanceInformationList: []ssmtypes.InstanceInformation{
				{InstanceId: aws.String("i-2"), PingStatus: ssmtypes.PingStatusOnline},
			},
		},
	}}
	efake := &fakeEC2{out: &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{
			{InstanceId: aws.String("i-1"), Tags: []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("web-1")}}},
			{InstanceId: aws.String("i-2"), Tags: []ec2types.Tag{{Key: aws.String("Name"), Value: aws.String("web-2")}}},
		}}},
	}}

	got, err := Fetch(context.Background(), sfake, efake, Filter{NamePrefix: "web-"}, SourceSSMOnly)
	if err != nil {
		t.Fatal(err)
	}
	if sfake.calls != 2 {
		t.Errorf("want 2 SSM pages fetched, got %d", sfake.calls)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 merged instances, got %d", len(got))
	}
	if efake.gotInput == nil || len(efake.gotInput.Filters) != 1 {
		t.Fatalf("EC2 filter not passed through")
	}
	if *efake.gotInput.Filters[0].Name != "tag:Name" {
		t.Errorf("want tag:Name filter, got %v", *efake.gotInput.Filters[0].Name)
	}
}

// blockingSSM blocks until the context is cancelled, simulating a hung endpoint.
type blockingSSM struct{}

func (blockingSSM) DescribeInstanceInformation(ctx context.Context, _ *ssm.DescribeInstanceInformationInput, _ ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestFetchRespectsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := Fetch(ctx, blockingSSM{}, &fakeEC2{out: &ec2.DescribeInstancesOutput{}}, Filter{}, SourceSSMOnly)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
}

func TestNaturalLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Group8-2", "Group8-10", true},  // numeric, not lexicographic
		{"Group8-10", "Group8-2", false}, // reverse
		{"Group8-9", "Group8-10", true},  // 9 before 10
		{"a1", "a10", true},              // mixed prefix
		{"a10", "b2", true},              // prefix wins over number
		{"alpha", "beta", true},          // pure strings stay alpha
		{"beta", "alpha", false},         // reverse
		{"Group8-2", "Group8-2", false},  // equal
		{"item-007", "item-8", true},     // leading zeros, 7 < 8
		{"item-2", "item-2x", true},      // numeric prefix then tail
	}
	for _, c := range cases {
		if got := naturalLess(c.a, c.b); got != c.want {
			t.Errorf("naturalLess(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSortNaturalOrder(t *testing.T) {
	in := []Instance{
		{Name: "Group8-10", SSMReady: true},
		{Name: "Group8-2", SSMReady: true},
		{Name: "Group8-1", SSMReady: true},
		{Name: "Group8-9", SSMReady: true},
	}
	Sort(in)
	want := []string{"Group8-1", "Group8-2", "Group8-9", "Group8-10"}
	for i, w := range want {
		if in[i].Name != w {
			t.Errorf("pos %d = %q, want %q", i, in[i].Name, w)
		}
	}
}

func TestSortPrecedenceAndIDFallback(t *testing.T) {
	in := []Instance{
		{InstanceID: "i-3", Name: "web-2", SSMReady: false},
		{InstanceID: "i-2", Name: "web-10", SSMReady: true},
		{InstanceID: "i-1", Name: "web-2", SSMReady: true, Pinned: true},
		{InstanceID: "mi-9"}, // empty name → sort by InstanceID, not ready
	}
	Sort(in)
	// pinned first, then SSMReady; within not-ready, name "mi-9" < "web-2".
	wantIDs := []string{"i-1", "i-2", "mi-9", "i-3"}
	for i, w := range wantIDs {
		if in[i].InstanceID != w {
			t.Errorf("pos %d = %q, want %q", i, in[i].InstanceID, w)
		}
	}
}
