// Copyright 2025.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// CloseOverlayMsg is sent when the user dismisses any overlay.
type CloseOverlayMsg struct{}

// HelpModel displays a scrollable keyboard shortcut overlay.
type HelpModel struct {
	lines  []string // pre-rendered plain lines (no ANSI truncation — raw styled)
	maxW   int      // max visible width across all lines
	offset int      // scroll offset
	width  int      // terminal width
	height int      // terminal height
}

var helpSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)

// buildHelpLines returns all help lines as a slice (one entry per visual line, no wrapping).
func buildHelpLines() []string {
	key := func(k string) string { return KeyStyle.Render(k) }
	dim := func(d string) string { return lipgloss.NewStyle().Foreground(colorDim).Render(d) }
	sec := func(s string) string { return helpSectionStyle.Render(s) }

	raw := []string{
		sec("Navigation"),
		"  " + key("j") + " / " + key("↓") + "    " + dim("Move selection down"),
		"  " + key("k") + " / " + key("↑") + "    " + dim("Move selection up"),
		"",
		sec("List Actions"),
		"  " + key("/") + "            " + dim("Start search / filter"),
		"  " + key("?") + "            " + dim("Show this help overlay"),
		"  " + key("Space") + "        " + dim("Quick preview of selected server"),
		"  " + key("s") + "            " + dim("Cycle sort mode"),
		"  " + key("r") + "            " + dim("Refresh server list"),
		"  " + key("q") + " / " + key("Ctrl+C") + "  " + dim("Quit"),
		"",
		sec("Server Actions"),
		"  " + key("Enter") + "        " + dim("Connect via SSH"),
		"  " + key("a") + "            " + dim("Add new server"),
		"  " + key("e") + "            " + dim("Edit selected server"),
		"  " + key("d") + "            " + dim("Delete selected server"),
		"  " + key("p") + "            " + dim("Toggle pin"),
		"  " + key("c") + "            " + dim("Copy SSH command to clipboard"),
		"  " + key("g") + "            " + dim("Ping selected server"),
		"  " + key("f") + "            " + dim("Open file browser (SCP)"),
		"",
		sec("File Browser"),
		"  " + key("Tab") + "          " + dim("Switch active panel (local ↔ remote)"),
		"  " + key("j") + " / " + key("k") + "      " + dim("Navigate files"),
		"  " + key("Enter") + "        " + dim("Enter directory"),
		"  " + key("h") + " / " + key("Backspace") + "  " + dim("Go up one directory"),
		"  " + key("u") + "            " + dim("Upload selected local file to remote"),
		"  " + key("d") + "            " + dim("Download selected remote file to local"),
		"  " + key("n") + "            " + dim("New directory"),
		"  " + key("x") + "            " + dim("Delete file/directory"),
		"  " + key("r") + "            " + dim("Rename"),
		"  " + key("Ctrl+C") + "       " + dim("Quit directly"),
		"  " + key("Esc") + "          " + dim("Exit file browser"),
		"",
		sec("Form (add/edit server)"),
		"  " + key("Tab") + " / " + key("Shift+Tab") + "  " + dim("Next / previous tab"),
		"  " + key("↑") + " / " + key("↓") + "          " + dim("Next / previous field"),
		"  " + key("Enter") + "               " + dim("Save with preview"),
		"  " + key("Esc") + "                " + dim("Cancel"),
		"",
		HelpStyle.Render("j/k or ↑/↓: scroll  •  any other key: close"),
	}
	return raw
}

// NewHelpModel creates a HelpModel.
func NewHelpModel(width, height int) HelpModel {
	lines := buildHelpLines()
	maxW := 0
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > maxW {
			maxW = w
		}
	}
	return HelpModel{lines: lines, maxW: maxW, width: width, height: height}
}

// Init implements tea.Model.
func (m HelpModel) Init() tea.Cmd { return nil }

// Update handles scrolling (j/k/up/down) and closes on any other key.
func (m HelpModel) Update(msg tea.Msg) (HelpModel, tea.Cmd) {
	if kp, ok := msg.(tea.KeyPressMsg); ok {
		switch kp.String() {
		case "j", "down":
			visH := m.visibleH()
			if m.offset < len(m.lines)-visH {
				m.offset++
			}
			return m, nil
		case "k", "up":
			if m.offset > 0 {
				m.offset--
			}
			return m, nil
		}
		return m, func() tea.Msg { return CloseOverlayMsg{} }
	}
	return m, nil
}

// visibleH returns how many content rows fit between the box borders.
// Box layout: top(1) + title(1) + sep(1) + visH + bottom(1) = visH+4 total rows.
// Target total = m.height-1 (leave 1 row for status bar) → visH = m.height-5.
func (m HelpModel) visibleH() int {
	vh := m.height - 5
	if vh < 3 {
		vh = 3
	}
	return vh
}

// ViewString renders the overlay as a plain string for embedding via overlayCenter.
// Draws the box manually (explicit border chars) so height is always exactly visH+4 rows.
func (m HelpModel) ViewString() string {
	visH := m.visibleH()

	// boxW = total columns occupied by the border box (border chars included).
	// inner = usable text columns inside: boxW-2 (left│ + right│) - 2 (1-space pad each side).
	boxW := m.maxW + 4 // text + 2 border + 2 padding
	if max := m.width - 2; boxW > max {
		boxW = max
	}
	if boxW < 34 {
		boxW = 34
	}
	inner := boxW - 4 // text columns available per row

	bStyle := lipgloss.NewStyle().Foreground(colorAmber)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(colorAccent)

	writeRow := func(sb *strings.Builder, content string) {
		vis := ansi.StringWidth(content)
		if vis > inner {
			content = ansi.Truncate(content, inner, "…")
			vis = inner
		}
		pad := inner - vis
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString(" ")
		sb.WriteString(content)
		if pad > 0 {
			sb.WriteString(strings.Repeat(" ", pad))
		}
		sb.WriteString(" ")
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString("\n")
	}

	var sb strings.Builder

	// Top border
	sb.WriteString(bStyle.Render("╭" + strings.Repeat("─", boxW-2) + "╮"))
	sb.WriteString("\n")

	// Title row + blank separator
	writeRow(&sb, titleStyle.Render("Key Bindings"))
	writeRow(&sb, "")

	// Content rows
	end := m.offset + visH
	if end > len(m.lines) {
		end = len(m.lines)
	}
	shown := 0
	for i := m.offset; i < end; i++ {
		writeRow(&sb, m.lines[i])
		shown++
	}
	for shown < visH {
		writeRow(&sb, "")
		shown++
	}

	// Bottom border — no trailing newline so overlayCenter counts lines correctly
	sb.WriteString(bStyle.Render("╰" + strings.Repeat("─", boxW-2) + "╯"))

	return sb.String()
}
