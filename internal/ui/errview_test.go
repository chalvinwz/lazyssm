package ui

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/charmbracelet/x/ansi"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/inventory"
	"github.com/chalvinwz/lazyssm/internal/preflight"
	"github.com/chalvinwz/lazyssm/internal/session"
)

// scpDenied is an SSM fetch denied by an SCP, in the format AWS documents.
func scpDenied() error {
	return &smithy.OperationError{
		ServiceID:     "SSM",
		OperationName: "DescribeInstanceInformation",
		Err: &awshttp.ResponseError{
			ResponseError: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 400}},
				Err: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:sts::123456789012:assumed-role/PowerUser/jane is not authorized to perform: " +
					"ssm:DescribeInstanceInformation on resource: arn:aws:ssm:ap-southeast-2:123456789012:* with an explicit deny in a service control policy: " +
					"arn:aws:organizations::777788889999:policy/o-exampleorgid/service_control_policy/p-examplepolicyid123"},
			},
			RequestID: "384132ca-0000-4000-8000-000000000000",
		},
	}
}

func denied(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t)
	m.width, m.height = 120, 24
	m.clients = awscfg.Clients{Cfg: aws.Config{Region: "ap-southeast-2"}}
	nm, _ := m.Update(instancesMsg{err: scpDenied()})
	return nm.(Model)
}

// deniedOverList is a refresh that failed after an earlier one worked, so the
// last-known instances are still listed.
func deniedOverList(t *testing.T) Model {
	t.Helper()
	m := loaded(t, sampleInstances())
	m.width, m.height = 120, 24
	m.clients = awscfg.Clients{Cfg: aws.Config{Region: "ap-southeast-2"}}
	nm, _ := m.Update(instancesMsg{err: scpDenied()})
	return nm.(Model)
}

func copyTo(m Model, label, copied *string) Model {
	m.copyText = func(l, text string) (tea.Cmd, bool) {
		*label, *copied = l, text
		return nil, false
	}
	return m
}

func TestFetchErrorExplained(t *testing.T) {
	m := denied(t)
	if m.status != "no SSM access in ap-southeast-2 (service control policy)" {
		t.Errorf("status = %q", m.status)
	}
	list := ansi.Strip(m.listContent(80, 20))
	for _, want := range []string{"✗ No SSM access in ap-southeast-2", "service control policy", "R  try another region", "e  show the full AWS error"} {
		if !strings.Contains(list, want) {
			t.Errorf("list pane should contain %q:\n%s", want, list)
		}
	}
	if strings.Contains(list, "arn:aws:sts") {
		t.Errorf("the list pane should explain, not dump the raw error:\n%s", list)
	}
}

func TestLongErrorKeepsFooterOneLine(t *testing.T) {
	m := denied(t)
	m.status = "fetch error: " + scpDenied().Error() // as long as the raw text
	for _, width := range []int{60, 96, 200} {
		m.width = width
		footer := m.footerBar()
		status := strings.Split(footer, "\n")[0]
		if lipgloss.Height(footer) != 2 || lipgloss.Width(status) > width {
			t.Errorf("at %d columns the footer should be status + hints, got %d lines, status %d wide", width, lipgloss.Height(footer), lipgloss.Width(status))
		}
		if !strings.Contains(ansi.Strip(status), "· e details") {
			t.Errorf("at %d columns the status should keep the e hint, got %q", width, ansi.Strip(status))
		}
	}
}

func TestErrorPanelShowsAndCopiesFullError(t *testing.T) {
	m := denied(t)
	var label, copied string
	m = copyTo(m, &label, &copied)
	m = press(m, "e")
	if !m.showErr {
		t.Fatal("e should open the error panel")
	}
	detail := ansi.Strip(m.detailContent(42, 22))
	for _, want := range []string{"AWS error", "AccessDeniedException", "p-examplepolicyid123", "y copy · e close"} {
		if !strings.Contains(detail, want) {
			t.Errorf("error panel should contain %q:\n%s", want, detail)
		}
	}
	if strings.Contains(ansi.Strip(m.footerBar()), "e details") {
		t.Error("the e hint is redundant while the panel is open")
	}
	m = press(m, "y")
	if label != errorLabel || copied != scpDenied().Error() {
		t.Errorf("y should copy the full error, got %q: %q", label, copied)
	}
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if nm.(Model).showErr {
		t.Error("esc should close the error panel")
	}
}

func TestErrorPanelOverLastKnownList(t *testing.T) {
	m := deniedOverList(t)
	var label, copied string
	m = copyTo(m, &label, &copied)
	if status := ansi.Strip(m.footerBar()); !strings.Contains(status, "(showing last-known) · e details") {
		t.Errorf("status should point at e, got %q", status)
	}
	if m = press(m, "y"); label != "instance ID" {
		t.Errorf("y copies the instance until the error is shown, got %q", label)
	}
	m = press(m, "e")
	if detail := ansi.Strip(m.detailContent(42, 22)); !strings.Contains(detail, "AccessDeniedException") {
		t.Errorf("e should show the error over the instance:\n%s", detail)
	}
	if m = press(m, "y"); label != errorLabel {
		t.Errorf("y should copy the error while it's shown, got %q", label)
	}
	if m = press(m, "e"); !strings.Contains(ansi.Strip(m.detailContent(42, 22)), "web-1") {
		t.Error("a second e should go back to the instance")
	}
}

func TestErrorPanelClosesWhenFetchWorks(t *testing.T) {
	m := press(denied(t), "e")
	nm, _ := m.Update(instancesMsg{items: sampleInstances()})
	m = nm.(Model)
	if m.showErr || m.err != nil {
		t.Error("a successful fetch should clear the error and close its panel")
	}
	if m = press(m, "e"); m.showErr || m.status != "no error to show" {
		t.Errorf("e without an error should say so, got showErr=%v status=%q", m.showErr, m.status)
	}
}

func TestErrorPanelFitsSmallPanel(t *testing.T) {
	m := press(denied(t), "e")
	detail := m.detailContent(24, 12)
	if lipgloss.Height(detail) > 12 || lipgloss.Width(detail) > 24 {
		t.Errorf("error panel overflows 24x12: %dx%d", lipgloss.Width(detail), lipgloss.Height(detail))
	}
	if !strings.HasSuffix(ansi.Strip(detail), "e close") {
		t.Errorf("the keys should stay visible when the text is cut:\n%s", ansi.Strip(detail))
	}
}

func ssoExpired(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t)
	m.profile = "acme-dev-network-admin"
	nm, _ := m.Update(instancesMsg{err: errors.New("operation error SSM: DescribeInstanceInformation, get identity: get credentials: failed to refresh cached credentials, the SSO session has expired or is invalid")})
	return nm.(Model)
}

func TestSSOExpiryShowsInline(t *testing.T) {
	m := ssoExpired(t)
	nm, _ := m.Update(preflightMsg{checks: expiredChecks()})
	m = nm.(Model)
	if m.showPreflight || ansi.Strip(m.render()) == ansi.Strip(m.renderPreflight()) {
		t.Fatal("an expired SSO session should not open the preflight checklist")
	}
	list := ansi.Strip(m.listContent(80, 20))
	for _, want := range []string{"✗ SSO session expired for acme-dev-network-admin", "L  sign in (aws sso login)", "P  try another profile"} {
		if !strings.Contains(list, want) {
			t.Errorf("list pane should contain %q:\n%s", want, list)
		}
	}
	if status := ansi.Strip(m.footerBar()); !strings.Contains(status, "SSO session expired for acme-dev-network-admin · L sign in") {
		t.Errorf("status should name the profile and L, got %q", status)
	}
}

func TestSignInKey(t *testing.T) {
	m := ssoExpired(t)
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'L', Text: "L"})
	m = nm.(Model)
	if cmd == nil || !m.loginInFlight {
		t.Fatal("L should start aws sso login") // the cmd is never run: it would exec aws
	}
	if _, cmd = m.Update(tea.KeyPressMsg{Code: 'L', Text: "L"}); cmd != nil {
		t.Error("a second L while signing in should not start another login")
	}
	nm, cmd = m.Update(session.LoginEndedMsg{})
	if m = nm.(Model); cmd == nil || m.loginInFlight || !m.loading {
		t.Error("after signing in lazyssm should re-check and reload")
	}
}

func TestPreflightOpensOnlyForMissingTools(t *testing.T) {
	m := newTestModel(t)
	missing := append(healthyChecks(), preflight.Check{Name: "session-manager-plugin", Binary: true, OK: false})
	nm, _ := m.Update(preflightMsg{checks: missing})
	m = nm.(Model)
	if m.showPreflight {
		t.Error("a missing plugin should not interrupt browsing")
	}
	m.all, m.visible = sampleInstances(), sampleInstances()
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !nm.(Model).showPreflight {
		t.Error("connecting without the plugin should open the checklist")
	}
}

// cliOutput is what aws ssm start-session printed before failing.
func cliOutput(code, message string) string {
	return "\nAn error occurred (" + code + ") when calling the StartSession operation: " + message + "\n"
}

func TestConnectFailureExplained(t *testing.T) {
	m := loaded(t, sampleInstances())
	var label, copied string
	m = copyTo(m, &label, &copied)
	out := cliOutput("TargetNotConnected", "i-1 is not connected.")
	nm, _ := m.Update(session.SessionEndedMsg{InstanceID: "i-1", Err: errors.New("exit status 254"), Output: out})
	m = nm.(Model)
	if m.status != "couldn't connect to web-1: instance not connected to SSM" {
		t.Errorf("status = %q", m.status)
	}
	if footer := ansi.Strip(m.footerBar()); !strings.Contains(footer, "· e details") || strings.Contains(footer, "last-known") {
		t.Errorf("footer = %q", footer)
	}
	m = press(m, "e")
	if detail := ansi.Strip(m.detailContent(42, 22)); !strings.Contains(detail, "TargetNotConnected") || !strings.Contains(detail, "i-1 is not connected") {
		t.Errorf("e should show what the CLI said:\n%s", detail)
	}
	if m = press(m, "y"); copied != strings.TrimSpace(out) {
		t.Errorf("y should copy the CLI's output, got %q", copied)
	}
	if nm, _ = m.Update(instancesMsg{items: sampleInstances()}); nm.(Model).failed != nil {
		t.Error("the next fetch should clear the connect failure")
	}
}

func TestConnectDeniedBySCP(t *testing.T) {
	m := loaded(t, sampleInstances())
	m.clients = awscfg.Clients{Cfg: aws.Config{Region: "ap-southeast-2"}}
	out := cliOutput("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/PowerUser/jane is not authorized to perform: ssm:StartSession "+
		"on resource: arn:aws:ec2:ap-southeast-2:123456789012:instance/i-1 with an explicit deny in a service control policy: "+
		"arn:aws:organizations::777788889999:policy/o-exampleorgid/service_control_policy/p-examplepolicyid123")
	nm, _ := m.Update(session.SessionEndedMsg{InstanceID: "i-1", Err: errors.New("exit status 254"), Output: out})
	if status := nm.(Model).status; status != "couldn't connect to web-1: no SSM access in ap-southeast-2 (service control policy)" {
		t.Errorf("status = %q", status)
	}
}

func TestSessionEndedNormally(t *testing.T) {
	nm, _ := loaded(t, sampleInstances()).Update(session.SessionEndedMsg{InstanceID: "i-1"})
	if m := nm.(Model); m.status != "session ended" || m.failed != nil {
		t.Errorf("status = %q failed = %v", m.status, m.failed)
	}
}

func TestSignInFailureKept(t *testing.T) {
	m := ssoExpired(t)
	m.loginInFlight = true
	nm, cmd := m.Update(session.LoginEndedMsg{Err: errors.New("exit status 255"),
		Output: "Attempting to open your browser\nError when retrieving token from sso: Token has expired and refresh failed\n"})
	m = nm.(Model)
	if cmd != nil || m.loading {
		t.Error("a failed sign-in should not reload: it would fail the same way and hide why")
	}
	if !strings.HasPrefix(m.status, "sign-in failed: SSO session expired") || !strings.Contains(ansi.Strip(m.footerBar()), "· L sign in") {
		t.Errorf("status = %q footer = %q", m.status, ansi.Strip(m.footerBar()))
	}
}

func TestSwitchFailureExplained(t *testing.T) {
	m := newTestModel(t)
	m.switchSeq = 1
	nm, _ := m.Update(switchedMsg{seq: 1, profile: "nope", err: config.SharedConfigProfileNotExistError{Profile: "nope"}})
	if status := nm.(Model).status; status != "switch failed: profile nope isn't in your AWS config" {
		t.Errorf("status = %q", status)
	}
}

func TestEC2DeniedOffersSSMOnly(t *testing.T) {
	m := newTestModel(t)
	m.src = inventory.SourceAllEC2
	err := &smithy.OperationError{ServiceID: "EC2", OperationName: "DescribeInstances",
		Err: &smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "You are not authorized to perform this operation."}}
	nm, _ := m.Update(instancesMsg{err: err})
	if list := ansi.Strip(nm.(Model).listContent(80, 20)); !strings.Contains(list, "t  show SSM nodes only") {
		t.Errorf("an EC2 denial in all-EC2 mode should offer t:\n%s", list)
	}
}
