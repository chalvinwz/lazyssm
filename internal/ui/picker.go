package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/sahilm/fuzzy"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/explain"
)

type pickerKind int

const (
	pickProfile pickerKind = iota
	pickRegion
)

// pickerItem is one choosable row: an AWS profile or a region.
type pickerItem struct {
	name   string      // profile name or region code
	note   string      // second column: where a profile opens, or the region's name
	region string      // for a profile: the region it opens in, "" to let the SDK resolve it
	detail [][2]string // key/value lines for the detail panel
	also   []string    // more text the filter matches: a region's name, a profile's account ID
}

// picker is the profile or region chooser. While it is open it takes over the
// list panel, the detail panel and the footer input.
type picker struct {
	kind    pickerKind
	items   []pickerItem
	visible []pickerItem
	current string // the active profile or region, marked in the list
	cursor  int
}

// switchedMsg carries freshly built AWS clients after a profile or region
// switch. seq ties it to the switch that issued it.
type switchedMsg struct {
	seq     int
	profile string
	clients awscfg.Clients
	err     error
}

// profileLabel names the default credential chain the way the AWS CLI does.
func profileLabel(profile string) string {
	if profile == "" {
		return "default"
	}
	return profile
}

func (m Model) openProfilePicker() (tea.Model, tea.Cmd) {
	profiles, err := m.listProfiles()
	if err != nil {
		m.status = "can't read AWS profiles: " + err.Error()
		return m, nil
	}
	if len(profiles) == 0 {
		m.status = "no profiles in ~/.aws/config or ~/.aws/credentials"
		return m, nil
	}
	items := make([]pickerItem, 0, len(profiles))
	for _, p := range profiles {
		region, why := m.regionFor(p)
		opens := region
		if region == "" {
			opens = p.Region // the SDK resolves it from the profile
		}
		if opens == "" {
			opens, why = "—", "no region set"
		}
		detail := [][2]string{{"opens", opens + " (" + why + ")"}}
		// The config region adds nothing unless lazyssm opens elsewhere.
		if p.Region != "" && p.Region != opens {
			detail = append(detail, [2]string{"config", p.Region})
		}
		items = append(items, pickerItem{
			name:   p.Name,
			note:   opens,
			region: region,
			detail: append(detail, [2]string{"account", p.AccountID}),
			also:   []string{p.AccountID},
		})
	}
	return m.openPicker(pickProfile, items, profileLabel(m.profile))
}

// regionFor picks the region a profile opens in: the one last browsed with it,
// else its own region setting, else the region on screen now. For its own
// setting it returns "" with reason "aws config", so the SDK resolves it.
func (m Model) regionFor(p awscfg.Profile) (region, why string) {
	if m.store != nil {
		if r := m.store.RegionFor(p.Name); r != "" {
			return r, "last used"
		}
	}
	if p.Region != "" {
		return "", "aws config"
	}
	return m.clients.Region(), "current region"
}

func (m Model) openRegionPicker() (tea.Model, tea.Cmd) {
	items := make([]pickerItem, 0, len(awscfg.Regions))
	for _, r := range awscfg.Regions {
		items = append(items, pickerItem{name: r.Code, note: r.Name, detail: [][2]string{{"name", r.Name}}, also: []string{r.Name}})
	}
	return m.openPicker(pickRegion, items, m.clients.Region())
}

func (m Model) openPicker(kind pickerKind, items []pickerItem, current string) (tea.Model, tea.Cmd) {
	m.picker = picker{kind: kind, items: items, visible: items, current: current}
	for i, it := range items {
		if it.name == current {
			m.picker.cursor = i
		}
	}
	m.mode = modePicker
	m.pickerInput.Reset()
	return m, m.pickerInput.Focus()
}

func (m Model) keyPicker(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.mode = modeList
		m.pickerInput.Blur()
		return m, nil
	case "enter":
		if len(m.picker.visible) == 0 {
			return m, nil
		}
		return m.applyPick(m.picker.visible[m.picker.cursor])
	case "up", "ctrl+p", "ctrl+k":
		m.picker.cursor = clamp(m.picker.cursor-1, 0, max(0, len(m.picker.visible)-1))
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.picker.cursor = clamp(m.picker.cursor+1, 0, max(0, len(m.picker.visible)-1))
		return m, nil
	}
	return m.updatePicker(msg)
}

// updatePicker feeds msg to the picker's input and refilters on a new query,
// starting again on the best match.
func (m Model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.pickerInput.Value()
	var cmd tea.Cmd
	m.pickerInput, cmd = m.pickerInput.Update(msg)
	if q := strings.TrimSpace(m.pickerInput.Value()); q != strings.TrimSpace(before) {
		m.picker.filter(q)
	}
	return m, cmd
}

// filter narrows the visible items to fuzzy matches of q. In the region
// picker a query shaped like a region code that is not listed is offered as
// its own row, so regions launched after this build can still be chosen.
func (p *picker) filter(q string) {
	p.cursor = 0
	if q == "" {
		p.visible = p.items
		return
	}
	var out []pickerItem
	if p.kind == pickRegion && awscfg.LooksLikeRegion(q) && !p.has(q) {
		out = append(out, pickerItem{name: q, note: "typed", detail: [][2]string{{"name", "not in lazyssm's region list"}}})
	}
	// Each field is matched alone, so a short query cannot string a match
	// together across them ("eu" from "us-east-1" and "US East"). Matches come
	// best first, ties in list order, so an item's first match is its best.
	var fields pickerSource
	for i, it := range p.items {
		for _, text := range append([]string{it.name}, it.also...) {
			if text == "" {
				continue
			}
			fields = append(fields, pickerField{i, text})
			if plain := awscfg.FoldAccents(text); plain != text {
				fields = append(fields, pickerField{i, plain})
			}
		}
	}
	seen := make(map[int]bool)
	for _, mt := range fuzzy.FindFrom(q, fields) {
		if i := fields[mt.Index].item; !seen[i] {
			seen[i] = true
			out = append(out, p.items[i])
		}
	}
	p.visible = out
}

func (p picker) has(name string) bool {
	for _, it := range p.items {
		if it.name == name {
			return true
		}
	}
	return false
}

// pickerField is one piece of text the filter matches, and the item it finds.
type pickerField struct {
	item int
	text string
}

// pickerSource adapts picker fields to fuzzy.Source.
type pickerSource []pickerField

func (s pickerSource) Len() int            { return len(s) }
func (s pickerSource) String(i int) string { return s[i].text }

// applyPick closes the picker and switches to the chosen profile or region.
func (m Model) applyPick(it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeList
	m.pickerInput.Blur()
	profile, region := m.profile, m.clients.Region()
	switch m.picker.kind {
	case pickProfile:
		if it.name == profileLabel(m.profile) {
			return m, nil
		}
		profile, region = it.name, it.region
	case pickRegion:
		if it.name == region {
			return m, nil
		}
		region = it.name
	}
	m.switchSeq++
	m.status = "switching to " + profileLabel(profile) + "…"
	return m, m.switchCmd(profile, region)
}

// switchCmd builds clients for profile and region off the UI thread. An empty
// region lets the SDK resolve the profile's own.
func (m Model) switchCmd(profile, region string) tea.Cmd {
	seq := m.switchSeq
	if m.demo {
		if region == "" {
			region = demoProfileRegion(profile)
		}
		return func() tea.Msg {
			return switchedMsg{seq: seq, profile: profile, clients: awscfg.Clients{Cfg: aws.Config{Region: region}}}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), preflightTimeout)
		defer cancel()
		clients, err := awscfg.Load(ctx, profile, region)
		return switchedMsg{seq: seq, profile: profile, clients: clients, err: err}
	}
}

// applySwitch installs the new clients and starts over: the old account's
// instances, preflight and login state no longer apply.
func (m Model) applySwitch(msg switchedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.switchSeq {
		return m, nil // a newer switch superseded this one
	}
	if msg.err != nil {
		m.status = "switch failed: " + explain.Error(msg.err, "").Status
		return m, nil
	}
	m.clients, m.profile, m.region = msg.clients, msg.profile, msg.clients.Region()
	m.gen++
	m.account = ""
	m.preflight = nil
	m.showPreflight = false
	m.loginAttempted = false
	m.err, m.showErr, m.failed = nil, false, nil
	m.unsaved = true
	m.all, m.visible, m.cursor = nil, nil, 0
	m.fetchSeq++
	m.loading = true
	m.status = fmt.Sprintf("switched to %s · %s", profileLabel(m.profile), m.clients.Region())
	if m.demo {
		return m, m.fetchCmd()
	}
	return m, tea.Batch(m.preflightCmd(), m.fetchCmd())
}

// saveSwitch remembers the switched-to profile and region once a fetch there
// has worked, so a region the account is denied in never becomes the one the
// profile, or the next launch, opens in.
func (m *Model) saveSwitch() {
	if !m.unsaved || m.store == nil {
		return
	}
	m.unsaved = false
	if err := m.store.RememberSwitch(m.profile, m.clients.Region()); err != nil {
		m.status += " (not saved: " + err.Error() + ")"
	}
}

// --- rendering ---

func (m Model) pickerList(w, h int) string {
	p := m.picker
	// Regions use the column names of AWS's region table: Code and Name.
	title, nameHead, noteHead := "profiles", "NAME", "OPENS IN"
	if p.kind == pickRegion {
		title, nameHead, noteHead = "regions", "CODE", "NAME"
	}
	styles := m.styles()
	nameW := 12
	for _, it := range p.visible {
		nameW = max(nameW, len(it.name))
	}
	nameW = min(nameW, max(1, w/2))
	header := styles.panelTitleStyle.Render(fmt.Sprintf("%s (%d)", title, len(p.visible)))
	header += "\n" + styles.dimStyle.Render("   "+padRight(nameHead, nameW)+" "+noteHead)
	if len(p.visible) == 0 {
		msg := "no match"
		if p.kind == pickRegion {
			msg = "no match — type a full region code, e.g. eu-west-1"
		}
		return header + "\n" + styles.offlineStyle.Render(truncate(msg, w))
	}

	rowsArea := max(1, h-2)
	start := 0
	if p.cursor >= rowsArea {
		start = p.cursor - rowsArea + 1
	}
	end := min(len(p.visible), start+rowsArea)
	lines := []string{header}
	for i := start; i < end; i++ {
		it := p.visible[i]
		mark := " "
		if it.name == p.current {
			mark = "●"
		}
		name := padRight(truncate(it.name, nameW), nameW)
		note := truncate(it.note, max(0, w-nameW-4-listRightPad))
		if i == p.cursor {
			lines = append(lines, styles.selStyle.Width(w).Render(padRight(" "+mark+" "+name+" "+note, w)))
			continue
		}
		lines = append(lines, " "+styles.onlineStyle.Render(mark)+" "+styles.nameStyle.Render(name)+" "+styles.dimStyle.Render(note))
	}
	return strings.Join(lines, "\n")
}

func (m Model) pickerDetail(w, h int) string {
	styles := m.styles()
	title := styles.panelTitleStyle.Render("detail")
	p := m.picker
	if p.cursor >= len(p.visible) {
		return title + "\n" + styles.dimStyle.Render("—")
	}
	it := p.visible[p.cursor]
	lines := []string{title, styles.detailHdrName.Render(truncate(it.name, w))}
	if it.name == p.current {
		lines = append(lines, styles.onlineStyle.Render("● current"))
	}
	lines = append(lines, "")
	for _, kv := range it.detail {
		v := kv[1]
		if v == "" {
			v = "—"
		}
		lines = append(lines, styles.detailKey.Render(padRight(kv[0], 8))+styles.detailValue.Render(truncate(v, w-8)))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (m Model) pickerFooter() string {
	label := "profile"
	if m.picker.kind == pickRegion {
		label = "region"
	}
	hint := m.styles().dimStyle.Render(" (↑↓ move · ⏎ switch · esc cancel)  ")
	return m.styles().keyStyle.Render(label) + hint + m.pickerInput.View()
}
