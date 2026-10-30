package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

type listColumn struct {
	name  string
	width int
}

// listRightPad keeps the last column off the panel's right border, as the
// pin gutter does on the left.
const listRightPad = 2

func (m Model) listColumns(w int) []listColumn {
	configured := []string{"name", "age", "ssm"}
	if m.store != nil && len(m.store.Columns) > 0 {
		configured = m.store.Columns
	}
	nameWidth := 16
	for _, in := range m.visible {
		nameWidth = max(nameWidth, lipgloss.Width(in.Name))
	}
	widths := map[string]int{"name": nameWidth, "age": 9, "ip": 15, "state": 13, "ssm": 3, "id": 19, "os": 7, "agent": 12, "ping": 14}
	seen := make(map[string]bool)
	columns := make([]listColumn, 0, len(configured)+1)
	for _, name := range configured {
		if width, ok := widths[name]; ok && !seen[name] {
			columns = append(columns, listColumn{name, width})
			seen[name] = true
		}
	}
	if len(columns) == 0 {
		columns = []listColumn{{"name", nameWidth}, {"age", 9}, {"ssm", 3}}
		seen["name"] = true
	}
	if !seen["name"] {
		columns = append([]listColumn{{"name", nameWidth}}, columns...)
	}
	needed := 3 + len(columns) - 1
	for _, column := range columns {
		needed += column.width
	}
	for i := len(columns) - 1; needed > w && i >= 0; i-- {
		if columns[i].name == "name" || columns[i].name == "ssm" {
			continue
		}
		needed -= columns[i].width + 1
		columns = append(columns[:i], columns[i+1:]...)
	}
	if needed > w {
		for i, column := range columns {
			if column.name == "ssm" {
				needed -= column.width + 1
				columns = append(columns[:i], columns[i+1:]...)
				break
			}
		}
	}
	// The padding comes out of spare room only: it never costs a column or a
	// character of a name.
	slack := w - needed
	pad := min(listRightPad, max(0, slack))
	for i := range columns {
		if columns[i].name == "name" {
			columns[i].width = max(1, nameWidth+slack-pad)
		}
	}
	return columns
}

func (m Model) rowLine(i int, in inventory.Instance, w int, columns []listColumn) string {
	selected := i == m.cursor
	styles := m.styles()
	pin := " "
	if in.Pinned {
		pin = "★"
	}
	if !selected {
		if in.Pinned {
			pin = styles.pinStyle.Render(pin)
		} else {
			pin = styles.faintStyle.Render(pin)
		}
	}
	parts := []string{" " + pin}
	for _, column := range columns {
		var value string
		style := styles.detailValue
		switch column.name {
		case "name":
			value, style = in.Name, styles.nameStyle
			if value == "" {
				value = "(no Name)"
			}
		case "age":
			value, style = instanceAge(in.LaunchTime, time.Now()), styles.dimStyle
		case "ip":
			value = in.IP
		case "state":
			value, style = instanceState(in.State), m.stateStyle(in.State)
		case "ssm":
			value, style = "○", styles.offlineStyle
			if in.SSMReady {
				value, style = "●", styles.onlineStyle
			}
		case "id":
			value = in.InstanceID
		case "os":
			value = in.PlatformType
		case "agent":
			value = in.AgentStatus
		case "ping":
			value = in.PingStatus
		}
		if value == "" {
			value = "—"
		}
		value = padRight(truncate(value, column.width), column.width)
		if !selected {
			value = style.Render(value)
		}
		parts = append(parts, value)
	}
	row := strings.Join(parts, " ")
	if selected {
		// Pad before styling so the highlight also covers the right padding.
		return styles.selStyle.Width(w).Render(padRight(row, w))
	}
	return row
}
