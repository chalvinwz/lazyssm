package inventory

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"testing"
	"time"
)

func TestLaunchTimeProjectionAndMerge(t *testing.T) {
	launch := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	e := fromEC2(ec2types.Instance{InstanceId: aws.String("i-test"), LaunchTime: &launch})
	for _, src := range []Source{SourceSSMOnly, SourceAllEC2} {
		got := merge([]ssmNode{{ID: "i-test", PingStatus: "Online"}}, []ec2Inst{e}, src, false)
		if len(got) != 1 || !got[0].LaunchTime.Equal(launch) {
			t.Fatalf("launch time lost: %+v", got)
		}
	}
	if !fromEC2(ec2types.Instance{}).LaunchTime.IsZero() {
		t.Fatal("missing launch time must stay unknown")
	}
	if !mergeOne("mi-test", nil, &ssmNode{ID: "mi-test"}).LaunchTime.IsZero() {
		t.Fatal("SSM-only node must have unknown age")
	}
}
