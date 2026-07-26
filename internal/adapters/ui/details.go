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

// DetailsModel displays the raw SSH config block for the selected server.
// It renders directly without a viewport component to avoid word-wrap issues.
type DetailsModel struct {
	rawContent string // original unprocessed SSH config block
	lines      []string // highlighted + truncated lines, rebuilt on each SetSize/SetRawConfig
	offset     int  // scroll offset
	width      int
	height     int
}

func NewDetailsModel() DetailsModel {
	return DetailsModel{}
}

// innerW returns the usable text width inside the border+padding.
// DetailsBorderStyle manual rendering: border(1+1) + padding(2+2) = 6 overhead.
// innerW = m.width - 6 so total box = innerW + 6 = m.width, filling rightW exactly.
func (m *DetailsModel) innerW() int {
	w := m.width - 6
	if w < 10 {
		w = 10
	}
	return w
}

// contentH returns the number of visible text rows inside the border.
// DetailsBorderStyle: Border(2) → vertical overhead = 2.
func (m *DetailsModel) contentH() int {
	h := m.height - 2 // border top + border bottom
	if h < 1 {
		h = 1
	}
	return h
}

func (m *DetailsModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	// Rebuild lines with the new truncation width.
	m.rebuildLines()
}

// SetRawConfig stores the raw SSH config and rebuilds highlighted lines.
func (m *DetailsModel) SetRawConfig(raw string) {
	m.rawContent = raw
	m.offset = 0
	m.rebuildLines()
}

// rebuildLines re-applies syntax highlighting + truncation for current width.
func (m *DetailsModel) rebuildLines() {
	tw := m.innerW()
	m.lines = nil

	if m.rawContent == "" {
		m.lines = []string{RawConfigCommentStyle.Render("No server selected")}
		return
	}

	for _, line := range strings.Split(m.rawContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			m.lines = append(m.lines, "")
			continue
		}
		var rendered string
		switch {
		case strings.HasPrefix(trimmed, "#"):
			rendered = RawConfigCommentStyle.Render(line)
		case strings.HasPrefix(trimmed, "Host ") || strings.HasPrefix(trimmed, "Match "):
			rendered = RawConfigKeywordStyle.Render(line)
		default:
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			idx := strings.IndexAny(trimmed, " \t")
			if idx > 0 {
				fieldName := trimmed[:idx]
				value := strings.TrimSpace(trimmed[idx:])
				rendered = indent +
					RawConfigFieldStyle.Render(fieldName) + " " +
					RawConfigValueStyle.Render(value)
			} else {
				rendered = RawConfigFieldStyle.Render(line)
			}
		}
		// Hard-truncate to innerW so the border never word-wraps.
		m.lines = append(m.lines, ansi.Truncate(rendered, tw, "…"))
	}
}

func (m DetailsModel) Init() tea.Cmd { return nil }

func (m DetailsModel) Update(msg tea.Msg) (DetailsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		ch := m.contentH()
		switch msg.String() {
		case "down", "j":
			if m.offset < len(m.lines)-ch {
				m.offset++
			}
		case "up", "k":
			if m.offset > 0 {
				m.offset--
			}
		}
	}
	return m, nil
}

func (m DetailsModel) View() tea.View {
	ch := m.contentH()
	tw := m.innerW()

	borderStyle := lipgloss.NewStyle().Foreground(colorAccent)

	// Build end:offset slice, exactly ch rows.
	end := m.offset + ch
	if end > len(m.lines) {
		end = len(m.lines)
	}

	var sb strings.Builder

	// ── top border ──────────────────────────────────────────────────────────
	sb.WriteString(borderStyle.Render("╭" + strings.Repeat("─", tw+4) + "╮"))
	sb.WriteString("\n")

	// ── content rows (exactly ch rows) ───────────────────────────────────
	shown := 0
	for i := m.offset; i < end && shown < ch; i++ {
		line := m.lines[i]
		// Pad to tw so empty lines fill the row properly.
		if vis := ansi.StringWidth(line); vis < tw {
			line += strings.Repeat(" ", tw-vis)
		}
		sb.WriteString(borderStyle.Render("│"))
		sb.WriteString("  ") // 2-char left padding
		sb.WriteString(line)
		sb.WriteString("  ") // 2-char right padding
		sb.WriteString(borderStyle.Render("│"))
		sb.WriteString("\n")
		shown++
	}
	// Fill remaining rows with blank lines.
	for shown < ch {
		sb.WriteString(borderStyle.Render("│"))
		sb.WriteString(strings.Repeat(" ", tw+4)) // tw + 2+2 padding
		sb.WriteString(borderStyle.Render("│"))
		sb.WriteString("\n")
		shown++
	}

	// ── bottom border ────────────────────────────────────────────────────
	sb.WriteString(borderStyle.Render("╰" + strings.Repeat("─", tw+4) + "╯"))

	return tea.NewView(sb.String())
}
