// Package ui implements the lazyssm Bubble Tea terminal interface.
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/clipboard"
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
)

// Network deadlines for background AWS work so a wedged endpoint can't leave the
// TUI spinning forever (only escape would be ctrl+c).
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

	width, height int
	loading       bool
	status        string
	err           error

	preflight     []preflight.Check
	showPreflight bool
	binariesOK    bool // cached from the last preflight run; avoids re-probing on connect

	autoLogin      bool // effective --auto-login (flag OR persisted config)
	loginInFlight  bool // `aws sso login` currently running via ExecProcess
	loginAttempted bool // the single attempt for the current expiry event is spent

	fetchSeq int  // bumped per issued fetch; replies with a stale seq are dropped
	demo     bool // LAZYSSM_DEMO: serve fixed sample data, never call AWS

	copyText     func(label, text string) (cmd tea.Cmd, viaTerminal bool) // clipboard.Copy outside tests
	startSession func(instanceID, profile, region string) tea.Cmd         // session.ConnectCmd outside tests
}

// instancesMsg carries the result of a background inventory fetch. seq ties the
// reply to the fetch that issued it so out-of-order results can be discarded.
type instancesMsg struct {
	seq     int
	items   []inventory.Instance
	warning string // what the fetch had to leave out, e.g. EC2 names
	err     error
}

// preflightMsg carries the result of background credential checks.
type preflightMsg struct {
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
		src:          inventory.SourceSSMOnly,
		mode:         modeList,
		loading:      true,
		filterInput:  fi,
		searchInput:  si,
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
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
		defer cancel()
		return preflightMsg{checks: preflight.Run(ctx, p)}
	}
}

// tryAutoLogin returns a LoginCmd when auto-login should fire for an SSO
// expiry, spending the single attempt for the current expiry event. It returns
// nil when disabled, in demo mode, the attempt is spent, or a login is already
// in flight. Update is single-threaded, so spending the attempt before the cmd
// runs keeps the guard race-free even when preflight and fetch both report
// expiry in the same batch.
func (m *Model) tryAutoLogin() tea.Cmd {
	if !m.autoLogin || m.demo || m.loginAttempted || m.loginInFlight {
		return nil
	}
	m.loginAttempted = true
	m.loginInFlight = true
	m.status = "SSO session expired — running aws sso login…"
	return session.LoginCmd(m.profile, m.region)
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
		if msg.err != nil {
			m.err = msg.err
			m.status = "fetch error: " + msg.err.Error()
			if preflight.IsSSOExpired(msg.err) {
				if cmd := m.tryAutoLogin(); cmd != nil {
					return m, cmd
				}
			}
			return m, nil
		}
		m.err = nil
		m.loginAttempted = false // a successful AWS call proves the expiry event is over
		m.applyPins(msg.items)
		m.all = msg.items
		m.recompute()
		m.status = fmt.Sprintf("%d instances", len(m.all))
		if msg.warning != "" {
			m.status += " · " + msg.warning
		}
		return m, nil

	case preflightMsg:
		m.preflight = msg.checks
		m.binariesOK = preflight.BinariesOKFrom(msg.checks)
		if m.loginInFlight {
			// Result predates the suspended login; LoginEndedMsg re-runs preflight.
			return m, nil
		}
		if !preflight.SSOExpiredFrom(msg.checks) {
			m.loginAttempted = false // expiry resolved; a later expiry gets a fresh attempt
		} else if cmd := m.tryAutoLogin(); cmd != nil {
			m.showPreflight = false // don't render the modal over the login handover
			return m, cmd
		}
		m.showPreflight = !preflight.AllOK(msg.checks)
		return m, nil

	case session.LoginEndedMsg:
		m.loginInFlight = false
		if msg.Err != nil {
			m.status = "sso login failed: " + msg.Err.Error()
		} else {
			m.status = "sso login finished — re-checking credentials…"
		}
		// Re-verify and refetch regardless of outcome; loginAttempted stays
		// spent, so a still-broken identity opens the modal instead of looping.
		m.fetchSeq++
		m.loading = true
		return m, tea.Batch(m.preflightCmd(), m.fetchCmd())

	case session.SessionEndedMsg:
		if msg.Err != nil {
			m.status = "session ended: " + msg.Err.Error()
		} else {
			m.status = "session ended"
		}
		return m, nil

	case clipboard.CopiedMsg:
		if msg.Err != nil {
			m.status = "copy failed: " + msg.Err.Error()
		} else {
			m.status = "copied " + msg.Text + " to clipboard"
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	// Route to the focused text input when in an input mode.
	return m.routeInput(msg)
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global quit.
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	// Preflight modal: dismiss with any key, re-check with r.
	if m.showPreflight {
		switch key {
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
		return m.keyFilter(msg, key)
	case modeSearch:
		return m.keySearch(msg, key)
	case modeHelp:
		m.mode = modeList
		return m, nil
	default:
		return m.keyList(key)
	}
}

func (m Model) keyList(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(0, len(m.visible)-1)
	case "?":
		m.mode = modeHelp
	case "r":
		m.fetchSeq++
		m.loading = true
		m.status = "refreshing…"
		return m, m.fetchCmd()
	case "t":
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
	case "p":
		return m.togglePin()
	case "y":
		return m.copySelected(false)
	case "Y":
		return m.copySelected(true)
	case "f":
		m.mode = modeFilter
		m.filterInput.SetValue(m.filter.String())
		return m, m.filterInput.Focus()
	case "/":
		m.mode = modeSearch
		m.searchInput.Reset()
		return m, m.searchInput.Focus()
	case "enter":
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
		// fzf-style: Enter connects the highlighted instance directly.
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
		// Use the cached preflight; if it hasn't run yet, kick one off to
		// populate the modal instead of blocking the UI thread re-probing.
		m.showPreflight = true
		if len(m.preflight) == 0 {
			return m, m.preflightCmd()
		}
		return m, nil
	}
	m.status = "connecting to " + inst.InstanceID + "…"
	// The resolved region rather than the --region flag, so the session opens in
	// the region whose inventory is on screen.
	return m, m.startSession(inst.InstanceID, m.profile, m.clients.Region())
}

// applyPins stamps pinned state from the store onto the given instances.
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

// recomputeKeeping rebuilds the visible slice by applying the fuzzy search
// query, then puts the cursor on the instance with ID keep, or clamps it when
// that instance is no longer visible.
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

// selectedID returns the instance ID under the cursor, or "" when there is none.
func (m Model) selectedID() string {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return ""
	}
	return m.visible[m.cursor].InstanceID
}

// rowSource adapts instances to fuzzy.Source.
//
// Match against the Name only (instance ID as fallback). Concatenating the
// instance ID or tags would let stray hex digits in the ID satisfy a digit in
// the pattern (e.g. "group7" matching every row via a "7" in some ID), turning
// the filter into a no-op reorder.
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
