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
	clients awscfg.Clients
	profile string
	region  string
	store   *store.Store

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

	fetchSeq int  // bumped per issued fetch; replies with a stale seq are dropped
	demo     bool // LAZYSSM_DEMO: serve fixed sample data, never call AWS
}

// instancesMsg carries the result of a background inventory fetch. seq ties the
// reply to the fetch that issued it so out-of-order results can be discarded.
type instancesMsg struct {
	seq   int
	items []inventory.Instance
	err   error
}

// preflightMsg carries the result of background credential checks.
type preflightMsg struct {
	checks []preflight.Check
}

// New constructs the root model.
func New(clients awscfg.Clients, st *store.Store, profile, region string) Model {
	fi := textinput.New()
	fi.SetVirtualCursor(true)
	fi.SetWidth(48)
	si := textinput.New()
	si.SetVirtualCursor(true)
	si.SetWidth(48)
	return Model{
		clients:     clients,
		profile:     profile,
		region:      region,
		store:       st,
		src:         inventory.SourceSSMOnly,
		mode:        modeList,
		loading:     true,
		filterInput: fi,
		searchInput: si,
	}
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
		items, err := inventory.Fetch(ctx, clients.SSM, clients.EC2, filter, src)
		return instancesMsg{seq: seq, items: items, err: err}
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
			return m, nil
		}
		m.err = nil
		m.applyPins(msg.items)
		m.all = msg.items
		m.recompute()
		m.status = fmt.Sprintf("%d instances", len(m.all))
		return m, nil

	case preflightMsg:
		m.preflight = msg.checks
		m.binariesOK = preflight.BinariesOKFrom(msg.checks)
		m.showPreflight = !preflight.AllOK(msg.checks)
		return m, nil

	case session.SessionEndedMsg:
		if msg.Err != nil {
			m.status = "session ended: " + msg.Err.Error()
		} else {
			m.status = "session ended"
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
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.recompute()
	return m, cmd
}

func (m Model) routeInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.mode {
	case modeFilter:
		m.filterInput, cmd = m.filterInput.Update(msg)
	case modeSearch:
		m.searchInput, cmd = m.searchInput.Update(msg)
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
	m.recompute()
	return m, nil
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
	return m, session.ConnectCmd(inst.InstanceID, m.profile, m.region)
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

// recompute rebuilds the visible slice by applying the fuzzy search query.
func (m *Model) recompute() {
	q := strings.TrimSpace(m.searchInput.Value())
	if q == "" {
		m.visible = m.all
		m.cursor = clamp(m.cursor, 0, max(0, len(m.visible)-1))
		return
	}
	src := rowSource(m.all)
	matches := fuzzy.FindFrom(q, src)
	out := make([]inventory.Instance, 0, len(matches))
	for _, mt := range matches {
		out = append(out, m.all[mt.Index])
	}
	m.visible = out
	m.cursor = clamp(m.cursor, 0, max(0, len(m.visible)-1))
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
