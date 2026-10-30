// Package ui implements the lazyssm Bubble Tea terminal interface.
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"
	"github.com/sahilm/fuzzy"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/clipboard"
	"github.com/chalvinwz/lazyssm/internal/explain"
	"github.com/chalvinwz/lazyssm/internal/inventory"
	"github.com/chalvinwz/lazyssm/internal/preflight"
	"github.com/chalvinwz/lazyssm/internal/session"
	"github.com/chalvinwz/lazyssm/internal/store"
)

type mode int

const (
	modeList mode = iota
	modeFilter
	modeSearch
	modeHelp
	modePicker
)

// Deadlines for background AWS calls, so a wedged endpoint cannot leave the
// TUI spinning with ctrl+c as the only way out.
const (
	fetchTimeout     = 30 * time.Second
	preflightTimeout = 15 * time.Second
)

// Model is the root Bubble Tea model for lazyssm.
type Model struct {
	clients    awscfg.Clients
	profile    string
	region     string
	store      *store.Store
	appearance *themeStyles

	all     []inventory.Instance // last fetched, server-filtered set
	visible []inventory.Instance // after fuzzy search
	cursor  int

	src    inventory.Source
	filter inventory.Filter

	mode        mode
	filterInput textinput.Model
	searchInput textinput.Model
	pickerInput textinput.Model
	picker      picker

	width, height int
	loading       bool
	status        string
	err           error
	explained     explain.Explanation  // err in plain words, set with it
	failed        *explain.Explanation // the last connect or sign-in failure, until the next fetch
	showErr       bool                 // e: the detail panel shows the full error

	preflight     []preflight.Check
	showPreflight bool
	binariesOK    bool   // cached from the last preflight run; avoids re-probing on connect
	account       string // AWS account ID from the last identity check

	autoLogin      bool // effective --auto-login (flag OR persisted config)
	loginInFlight  bool // `aws sso login` currently running via ExecProcess
	loginAttempted bool // the single attempt for the current expiry event is spent

	fetchSeq  int  // bumped per issued fetch; replies with a stale seq are dropped
	switchSeq int  // bumped per issued profile/region switch, likewise
	gen       int  // bumped per completed switch; preflight replies for older clients are dropped
	unsaved   bool // the switched-to profile/region is saved once a fetch there works
	demo      bool // LAZYSSM_DEMO: serve fixed sample data, never call AWS

	copyText     func(label, text string) (cmd tea.Cmd, viaTerminal bool) // clipboard.Copy outside tests
	startSession func(instanceID, profile, region string) tea.Cmd         // session.ConnectCmd outside tests
	listProfiles func() ([]awscfg.Profile, error)                         // awscfg.Profiles outside tests and demo
}

// instancesMsg carries the result of a background inventory fetch. seq ties the
// reply to the fetch that issued it so out-of-order results can be discarded.
type instancesMsg struct {
	seq     int
	items   []inventory.Instance
	warning string // what the fetch had to leave out, e.g. EC2 names
	err     error
}

// preflightMsg carries the result of background credential checks. gen ties it
// to the clients it checked, so a result from before a switch is discarded.
type preflightMsg struct {
	gen    int
	checks []preflight.Check
}

// New constructs the root model.
func New(clients awscfg.Clients, st *store.Store, profile, region string, autoLogin bool) Model {
	fi := textinput.New()
	fi.SetVirtualCursor(true)
	fi.SetWidth(48)
	si := textinput.New()
	si.SetVirtualCursor(true)
	si.SetWidth(48)
	pi := textinput.New()
	pi.SetVirtualCursor(true)
	pi.SetWidth(32)
	theme := "default"
	if st != nil {
		theme = st.Theme
	}
	return Model{
		clients:      clients,
		profile:      profile,
		region:       region,
		store:        st,
		autoLogin:    autoLogin,
		copyText:     clipboard.Copy,
		startSession: session.ConnectCmd,
		listProfiles: awscfg.Profiles,
		src:          inventory.SourceSSMOnly,
		mode:         modeList,
		loading:      true,
		filterInput:  fi,
		searchInput:  si,
		pickerInput:  pi,
	}.WithTheme(theme)
}

// Init kicks off the initial fetch and credential preflight.
func (m Model) Init() tea.Cmd {
	if m.demo {
		return m.fetchCmd() // no AWS, no preflight in demo mode
	}
	return tea.Batch(m.fetchCmd(), m.preflightCmd())
}

func (m Model) fetchCmd() tea.Cmd {
	seq := m.fetchSeq
	if m.demo {
		filter, src := m.filter, m.src
		return func() tea.Msg {
			return instancesMsg{seq: seq, items: filterDemo(demoInstances(), filter, src)}
		}
	}
	clients, filter, src := m.clients, m.filter, m.src
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		items, warning, err := inventory.Fetch(ctx, clients.SSM, clients.EC2, filter, src)
		return instancesMsg{seq: seq, items: items, warning: warning, err: err}
	}
}

func (m Model) preflightCmd() tea.Cmd {
	p := preflight.Params{Profile: m.profile, Region: m.clients.Region(), STS: m.clients.STS}
	gen := m.gen
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
		defer cancel()
		return preflightMsg{gen: gen, checks: preflight.Run(ctx, p)}
	}
}

// tryAutoLogin returns a LoginCmd for an SSO expiry, or nil when auto-login is
// off, in demo mode, already tried for this expiry, or a login is in flight.
// Update is single-threaded, so spending the attempt before the cmd runs keeps
// the guard race-free when preflight and fetch report the same expiry at once.
func (m *Model) tryAutoLogin() tea.Cmd {
	if !m.autoLogin || m.demo || m.loginAttempted || m.loginInFlight {
		return nil
	}
	m.loginAttempted = true
	m.loginInFlight = true
	m.status = "SSO session expired — running aws sso login…"
	return session.LoginCmd(m.profile, m.region)
}

// signIn suspends the TUI to run aws sso login for the current profile. On
// success, LoginEndedMsg re-checks credentials and reloads the list.
func (m Model) signIn() (tea.Model, tea.Cmd) {
	if m.demo {
		m.status = "demo: would run aws sso login --profile " + profileLabel(m.profile)
		return m, nil
	}
	if m.loginInFlight {
		return m, nil
	}
	m.loginInFlight = true
	m.failed = nil
	m.status = "signing in with aws sso login…"
	return m, session.LoginCmd(m.profile, m.region)
}

// showFailure explains a failed connect or sign-in in the status line and keeps
// what the CLI printed for the error panel, where e shows it and y copies it.
func (m *Model) showFailure(prefix string, err error, output string) {
	x := explain.Error(err, m.clients.Region())
	if out := strings.TrimSpace(output); out != "" {
		x.Raw = out
	}
	m.failed, m.showErr = &x, false
	m.status = prefix + ": " + x.Status
}

// instanceName names an instance for the status line, by its Name tag when it
// has one.
func (m Model) instanceName(id string) string {
	for _, in := range m.all {
		if in.InstanceID == id && in.Name != "" {
			return in.Name
		}
	}
	return id
}

// needsSignIn reports whether the last fetch failed because the SSO session
// expired, by its own error or by the identity check run beside it.
func (m Model) needsSignIn() bool {
	return m.err != nil && (m.explained.SignIn || preflight.SSOExpiredFrom(m.preflight))
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case instancesMsg:
		if msg.seq != m.fetchSeq {
			return m, nil // a newer fetch superseded this reply
		}
		m.loading = false
		m.failed = nil // the fetch result supersedes the older failure
		if msg.err != nil {
			m.err = msg.err
			m.explained = explain.Error(msg.err, m.clients.Region())
			m.status = m.explained.Status
			if m.needsSignIn() {
				m.status = "SSO session expired for " + profileLabel(m.profile)
			}
			if preflight.IsSSOExpired(msg.err) {
				if cmd := m.tryAutoLogin(); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		}
		m.err, m.showErr = nil, false
		m.loginAttempted = false // a successful AWS call proves the expiry event is over
		m.applyPins(msg.items)
		m.all = msg.items
		m.recompute()
		m.status = fmt.Sprintf("%d instances", len(m.all))
		if msg.warning != "" {
			m.status += " · " + msg.warning
		}
		m.saveSwitch()
		return m, nil

	case preflightMsg:
		if msg.gen != m.gen {
			return m, nil // checked the clients from before a switch
		}
		m.preflight = msg.checks
		m.binariesOK = preflight.BinariesOKFrom(msg.checks)
		m.account = preflight.AccountFrom(msg.checks)
		if m.loginInFlight {
			// The result predates the suspended login, and LoginEndedMsg
			// re-runs preflight.
			return m, nil
		}
		if !preflight.SSOExpiredFrom(msg.checks) {
			m.loginAttempted = false // expiry resolved; a later expiry gets a fresh attempt
		} else if cmd := m.tryAutoLogin(); cmd != nil {
			m.showPreflight = false // keep the checklist off the login handover
			return m, cmd
		}
		// Credential and region problems show in the list panel, where the
		// failed fetch explains them. The checklist only opens on connect, for
		// a missing aws CLI or plugin, and closes once a re-check finds them.
		if m.showPreflight {
			m.showPreflight = !m.binariesOK
		}
		return m, nil

	case session.LoginEndedMsg:
		m.loginInFlight = false
		if msg.Err != nil {
			// Nothing was refreshed, so a reload would only fail the same way
			// and replace the reason. loginAttempted stays spent, so
			// auto-login does not loop either.
			m.showFailure("sign-in failed", session.CLIError("SSO", msg.Output, msg.Err), msg.Output)
			return m, nil
		}
		m.status = "signed in — re-checking credentials…"
		m.fetchSeq++
		m.loading = true
		return m, tea.Batch(m.preflightCmd(), m.fetchCmd())

	case session.SessionEndedMsg:
		if msg.Err == nil {
			m.status = "session ended"
			return m, nil
		}
		err := session.CLIError("SSM", msg.Output, msg.Err)
		prefix := "session with " + m.instanceName(msg.InstanceID) + " ended"
		var op *smithy.OperationError
		if errors.As(err, &op) && op.OperationName == "StartSession" {
			prefix = "couldn't connect to " + m.instanceName(msg.InstanceID)
		}
		m.showFailure(prefix, err, msg.Output)
		return m, nil

	case switchedMsg:
		return m.applySwitch(msg)

	case clipboard.CopiedMsg:
		if msg.Err != nil {
			m.status = "copy failed: " + msg.Err.Error()
		} else {
			what := msg.Text
			if msg.Label == errorLabel {
				what = "the " + errorLabel
			}
			m.status = "copied " + what + " to clipboard"
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	// Route to the focused text input when in an input mode.
	return m.routeInput(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pressed := msg.String()

	// ctrl+c quits from every mode, the preflight modal included.
	if pressed == "ctrl+c" {
		return m, tea.Quit
	}

	// The preflight modal takes every key: r re-checks, and esc, q or enter
	// dismiss it.
	if m.showPreflight {
		switch pressed {
		case "r":
			m.fetchSeq++
			m.loading = true
			return m, tea.Batch(m.preflightCmd(), m.fetchCmd())
		case "esc", "q", "enter":
			m.showPreflight = false
		}
		return m, nil
	}

	switch m.mode {
	case modeFilter:
		return m.keyFilter(msg, pressed)
	case modeSearch:
		return m.keySearch(msg, pressed)
	case modePicker:
		return m.keyPicker(msg, pressed)
	case modeHelp:
		m.mode = modeList
		return m, nil
	default:
		return m.keyList(msg)
	}
}

func (m Model) keyList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Quit.Binding):
		return m, tea.Quit
	case key.Matches(msg, keys.Down.Binding):
		m.moveCursor(1)
	case key.Matches(msg, keys.Up.Binding):
		m.moveCursor(-1)
	case key.Matches(msg, keys.Top.Binding):
		m.cursor = 0
	case key.Matches(msg, keys.Bottom.Binding):
		m.cursor = max(0, len(m.visible)-1)
	case key.Matches(msg, keys.Help.Binding):
		m.mode = modeHelp
	case key.Matches(msg, keys.Refresh.Binding):
		m.fetchSeq++
		m.loading = true
		m.status = "refreshing…"
		return m, m.fetchCmd()
	case key.Matches(msg, keys.Toggle.Binding):
		if m.src == inventory.SourceSSMOnly {
			m.src = inventory.SourceAllEC2
			m.status = "source: all EC2"
		} else {
			m.src = inventory.SourceSSMOnly
			m.status = "source: SSM only"
		}
		m.fetchSeq++
		m.loading = true
		return m, m.fetchCmd()
	case key.Matches(msg, keys.Pin.Binding):
		return m.togglePin()
	case key.Matches(msg, keys.Profile.Binding):
		return m.openProfilePicker()
	case key.Matches(msg, keys.Region.Binding):
		return m.openRegionPicker()
	case key.Matches(msg, keys.SignIn.Binding):
		return m.signIn()
	case key.Matches(msg, keys.Error.Binding):
		if _, ok := m.lastError(); !ok {
			m.status = "no error to show"
			return m, nil
		}
		m.showErr = !m.showErr
	case key.Matches(msg, keys.CloseError.Binding):
		m.showErr = false
	case key.Matches(msg, keys.CopyID.Binding):
		if m.errorShown() {
			return m.copyError()
		}
		return m.copySelected(false)
	case key.Matches(msg, keys.CopyIP.Binding):
		return m.copySelected(true)
	case key.Matches(msg, keys.Filter.Binding):
		m.mode = modeFilter
		m.filterInput.SetValue(m.filter.String())
		return m, m.filterInput.Focus()
	case key.Matches(msg, keys.Search.Binding):
		m.mode = modeSearch
		m.searchInput.Reset()
		return m, m.searchInput.Focus()
	case key.Matches(msg, keys.Connect.Binding):
		return m.connect()
	}
	return m, nil
}

func (m Model) keyFilter(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.mode = modeList
		m.filterInput.Blur()
		return m, nil
	case "enter":
		f, ignored := parseFilter(m.filterInput.Value())
		m.filter = f
		m.mode = modeList
		m.filterInput.Blur()
		m.fetchSeq++
		m.loading = true
		if len(ignored) > 0 {
			m.status = "ignored: " + strings.Join(ignored, " ")
		} else {
			m.status = "filtering…"
		}
		return m, m.fetchCmd()
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	return m, cmd
}

func (m Model) keySearch(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		// Clear the search and return to the full list.
		m.mode = modeList
		m.searchInput.Reset()
		m.searchInput.Blur()
		m.recompute()
		return m, nil
	case "enter":
		// As in fzf, enter connects to the highlighted instance directly.
		m.mode = modeList
		m.searchInput.Blur()
		return m.connect()
	case "up", "ctrl+p", "ctrl+k":
		// Navigate the filtered list while still typing.
		m.moveCursor(-1)
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.moveCursor(1)
		return m, nil
	}
	return m.updateSearch(msg)
}

// updateSearch feeds msg to the search input. A changed query re-ranks the
// matches, so the cursor starts again on the best one.
func (m Model) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.searchInput.Value()
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	if m.searchInput.Value() != before {
		m.recomputeKeeping("")
		m.cursor = 0
	}
	return m, cmd
}

func (m Model) routeInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.mode {
	case modeList, modeHelp:
		// no text input has focus
	case modeFilter:
		m.filterInput, cmd = m.filterInput.Update(msg)
	case modeSearch:
		return m.updateSearch(msg) // e.g. a paste, which arrives as its own msg
	case modePicker:
		return m.updatePicker(msg)
	}
	return m, cmd
}

func (m *Model) moveCursor(delta int) {
	if len(m.visible) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, len(m.visible)-1)
}

func (m Model) togglePin() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.visible) {
		return m, nil
	}
	id := m.visible[m.cursor].InstanceID
	if m.store != nil {
		if _, err := m.store.TogglePin(id); err != nil {
			m.status = "pin error: " + err.Error()
			return m, nil
		}
	}
	m.applyPins(m.all)
	// applyPins re-sorted m.all in place, and m.visible may share its backing
	// array, so the selection must be restored by the ID captured above.
	m.recomputeKeeping(id)
	return m, nil
}

// copySelected copies the selected instance's ID, or its IP when wantIP.
func (m Model) copySelected(wantIP bool) (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.visible) {
		return m, nil
	}
	inst := m.visible[m.cursor]
	label, text := "instance ID", inst.InstanceID
	if wantIP {
		label, text = "IP", inst.IP
	}
	if text == "" {
		name := inst.Name
		if name == "" {
			name = inst.InstanceID
		}
		m.status = name + " has no " + label
		return m, nil
	}
	cmd, viaTerminal := m.copyText(label, text)
	if viaTerminal {
		// OSC 52 is fire-and-forget, so there is no CopiedMsg to wait for.
		m.status = "sent " + text + " to the terminal clipboard"
	}
	return m, cmd
}

func (m Model) connect() (tea.Model, tea.Cmd) {
	if m.cursor >= len(m.visible) {
		return m, nil
	}
	inst := m.visible[m.cursor]
	if m.demo {
		m.status = "demo: would connect to " + inst.InstanceID
		return m, nil
	}
	if !inst.SSMReady {
		m.status = inst.InstanceID + " is not SSM-ready (no online agent)"
		return m, nil
	}
	if !m.binariesOK {
		// Use the cached preflight. If none has run yet, start one to fill the
		// modal rather than block the UI thread re-probing.
		m.showPreflight = true
		if len(m.preflight) == 0 {
			return m, m.preflightCmd()
		}
		return m, nil
	}
	m.failed = nil
	m.status = "connecting to " + inst.InstanceID + "…"
	// Pass the resolved region, not the --region flag, so the session opens in
	// the region whose inventory is on screen.
	return m, m.startSession(inst.InstanceID, m.profile, m.clients.Region())
}

// applyPins stamps pinned state from the store onto items and re-sorts them in
// place.
func (m Model) applyPins(items []inventory.Instance) {
	if m.store == nil {
		return
	}
	for i := range items {
		items[i].Pinned = m.store.IsPinned(items[i].InstanceID)
	}
	inventory.Sort(items)
}

// recompute rebuilds the visible slice, keeping the cursor on the selected
// instance while it stays visible.
func (m *Model) recompute() { m.recomputeKeeping(m.selectedID()) }

// recomputeKeeping rebuilds the visible slice from the search query and puts
// the cursor on the instance with ID keep, or clamps it when keep is not shown.
func (m *Model) recomputeKeeping(keep string) {
	q := strings.TrimSpace(m.searchInput.Value())
	if q == "" {
		m.visible = m.all
	} else {
		matches := fuzzy.FindFrom(q, rowSource(m.all))
		out := make([]inventory.Instance, 0, len(matches))
		for _, mt := range matches {
			out = append(out, m.all[mt.Index])
		}
		m.visible = out
	}
	if keep != "" {
		for i := range m.visible {
			if m.visible[i].InstanceID == keep {
				m.cursor = i
				return
			}
		}
	}
	m.cursor = clamp(m.cursor, 0, max(0, len(m.visible)-1))
}

// selectedID returns the instance ID under the cursor, or "" if there is none.
func (m Model) selectedID() string {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return ""
	}
	return m.visible[m.cursor].InstanceID
}

// rowSource adapts instances to fuzzy.Source. It matches the Name alone, or the
// instance ID when there is no Name: with IDs or tags included, their hex
// digits would match any digit in the query, so "group7" would keep every row.
type rowSource []inventory.Instance

func (r rowSource) Len() int { return len(r) }
func (r rowSource) String(i int) string {
	if r[i].Name != "" {
		return r[i].Name
	}
	return r[i].InstanceID
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
