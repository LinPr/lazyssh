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
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// renderSessionView renders the full session view: a tab strip on top, the
// active terminal below it, and the status bar at the bottom.
func (m AppModel) renderSessionView() string {
	bodyH := m.height - 1 - 1 // status bar + tab strip
	if bodyH < 3 {
		bodyH = 3
	}

	tabs := m.sessions.renderTabStrip(m.width)
	term := m.sessions.renderTerminal(m.width, bodyH)
	statusBar := m.sessionStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, tabs, term, statusBar)
}

// sessionStatusBar shows the scroll position while browsing history, and the
// normal status bar otherwise.
func (m AppModel) sessionStatusBar() string {
	lines := 0
	if tab := m.sessions.activeTab(); tab != nil {
		lines = tab.scrolledLines()
	}
	if lines <= 0 {
		return m.statusBar.View(m.width)
	}
	hint := fmt.Sprintf("  ↑ %d lines of history (wheel / PgDn / Esc to return) ", lines)
	return TabInactiveStyle.Render(hint)
}

// renderTabStrip draws one line of session tabs, tmux-style.
func (m SessionsModel) renderTabStrip(width int) string {
	bStyle := lipgloss.NewStyle().Foreground(colorBorder)
	var parts []string
	for i, t := range m.tabs {
		dot := "●"
		if t.isClosed() {
			dot = "○"
		}
		label := fmt.Sprintf(" %s %d:%s ", dot, i+1, t.server.Alias)
		if i == m.active {
			parts = append(parts, TabActiveStyle.Render(label))
		} else {
			parts = append(parts, TabInactiveStyle.Render(label))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, TabInactiveStyle.Render(" no sessions "))
	}
	strip := strings.Join(parts, bStyle.Render("│"))
	if pad := width - ansi.StringWidth(strip); pad > 0 {
		strip += strings.Repeat(" ", pad)
	} else {
		strip = ansi.Truncate(strip, width, "…")
	}
	return strip
}

// renderTerminal draws the right panel containing the active session's terminal output.
func (m SessionsModel) renderTerminal(w, h int) string {
	tab := m.activeTab()
	if tab == nil {
		return renderEmptyTerminal(w, h)
	}
	if tab.isClosed() {
		return renderClosedTerminal(w, h, tab.server.Alias)
	}
	return tab.renderGrid(w, h)
}

func renderEmptyTerminal(w, h int) string {
	msg := HelpStyle.Render("Press o on a server to open a session")
	lines := make([]string, h)
	mid := h / 2
	for i := range lines {
		if i == mid {
			pad := (w - ansi.StringWidth("Press o on a server to open a session")) / 2
			if pad < 0 {
				pad = 0
			}
			lines[i] = strings.Repeat(" ", pad) + msg
		} else {
			lines[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(lines, "\n")
}

func renderClosedTerminal(w, h int, alias string) string {
	msg := StatusErrorStyle.Render("Session closed: " + alias)
	lines := make([]string, h)
	mid := h / 2
	for i := range lines {
		if i == mid {
			lines[i] = msg
		} else {
			lines[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(lines, "\n")
}

// renderGrid draws the terminal with the viewport scrolled back by
// scrollOffset lines, so the wheel can browse history.
func (t *SessionTab) renderGrid(w, h int) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	cols, rows := t.term.Width(), t.term.Height()
	if cols > w {
		cols = w
	}
	if rows > h {
		rows = h
	}

	sb := t.term.Scrollback()
	sbLen := 0
	if sb != nil {
		sbLen = sb.Len()
	}

	offset := t.scrollOffset
	if offset > sbLen {
		offset = sbLen
	}

	// Total addressable lines: history followed by the live screen.
	total := sbLen + rows
	// Top line of the viewport, measured from the oldest history line.
	first := total - rows - offset

	cur := t.term.CursorPosition()
	showCursor := offset == 0 && !t.term.CursorHidden()

	var out strings.Builder
	out.Grow(rows * (cols + 16))

	for row := 0; row < h; row++ {
		if row > 0 {
			out.WriteByte('\n')
		}
		line := first + row
		if line < 0 || line >= total {
			out.WriteString(strings.Repeat(" ", w))
			continue
		}
		if line < sbLen {
			out.WriteString(renderCells(sb.Line(line), cols, -1, row, false))
			continue
		}
		y := line - sbLen
		cursorX := -1
		if showCursor && y == cur.Y {
			cursorX = cur.X
		}
		out.WriteString(renderScreenRow(t.term, y, cols, cursorX))
	}

	// The loop above already emitted exactly h lines; adding more here would
	// push the status bar off screen.
	return out.String()
}

// renderScreenRow renders one live row, optionally inverting one cell as the cursor.
func renderScreenRow(term *vt.Emulator, y, cols, cursorX int) string {
	var sb strings.Builder
	var run strings.Builder
	var runStyle *uv.Style
	runCursor := false

	flush := func() {
		if run.Len() == 0 {
			return
		}
		st := lipgloss.NewStyle()
		if runStyle != nil {
			st = cellStyle(*runStyle)
		}
		if runCursor {
			st = st.Reverse(true)
		}
		sb.WriteString(st.Render(run.String()))
		run.Reset()
	}

	for x := 0; x < cols; x++ {
		cell := term.CellAt(x, y)
		if cell == nil {
			break
		}
		if cell.Width == 0 {
			// Continuation of a wide glyph: its width is already accounted for.
			continue
		}
		cursor := x == cursorX
		if runStyle == nil || !runStyle.Equal(&cell.Style) || cursor != runCursor {
			flush()
			st := cell.Style
			runStyle = &st
			runCursor = cursor
		}
		if cell.Content == "" {
			run.WriteByte(' ')
		} else {
			run.WriteString(cell.Content)
		}
	}
	flush()
	return sb.String()
}

// renderCells renders a stored scrollback line.
func renderCells(line uv.Line, cols, cursorX, row int, showCursor bool) string {
	var sb strings.Builder
	var run strings.Builder
	var runStyle *uv.Style

	flush := func() {
		if run.Len() == 0 {
			return
		}
		st := lipgloss.NewStyle()
		if runStyle != nil {
			st = cellStyle(*runStyle)
		}
		sb.WriteString(st.Render(run.String()))
		run.Reset()
	}

	for x := 0; x < cols; x++ {
		var cell uv.Cell
		if x < len(line) {
			cell = line[x]
		}
		if cell.Width == 0 && x < len(line) {
			continue
		}
		if runStyle == nil || !runStyle.Equal(&cell.Style) {
			flush()
			st := cell.Style
			runStyle = &st
		}
		if cell.Content == "" {
			run.WriteByte(' ')
		} else {
			run.WriteString(cell.Content)
		}
	}
	flush()

	// Pad to the pane width so borders stay aligned.
	if pad := cols - ansi.StringWidth(sb.String()); pad > 0 {
		sb.WriteString(strings.Repeat(" ", pad))
	}
	return sb.String()
}

// cellStyle converts a terminal cell style into a lipgloss style.
func cellStyle(s uv.Style) lipgloss.Style {
	st := lipgloss.NewStyle()
	if s.Fg != nil {
		st = st.Foreground(s.Fg)
	}
	if s.Bg != nil {
		st = st.Background(s.Bg)
	}
	if s.Attrs&uv.AttrBold != 0 {
		st = st.Bold(true)
	}
	if s.Attrs&uv.AttrItalic != 0 {
		st = st.Italic(true)
	}
	if s.Attrs&uv.AttrReverse != 0 {
		st = st.Reverse(true)
	}
	if s.Attrs&uv.AttrStrikethrough != 0 {
		st = st.Strikethrough(true)
	}
	if s.Underline != 0 {
		st = st.Underline(true)
	}
	return st
}

// renderTerminalOverlay renders a clean bordered terminal panel.
// Layout: top-border(1) + content(termH) + bottom-border(1) = h rows total.
// focused=true → accent border; false → dim border (left panel navigable).
func renderTerminalOverlay(tab *SessionTab, w, h int, focused bool) string {
	borderColor := colorBorder
	if focused {
		borderColor = colorAccent
	}
	bStyle := lipgloss.NewStyle().Foreground(borderColor)

	// inner = w-2: border overhead = 2, box total = w = rightW, matching details panel.
	inner := w - 2
	if inner < 10 {
		inner = 10
	}

	// Terminal content fills h - 2 rows (top-border + bottom-border = 2 overhead).
	termH := h - 2
	if termH < 1 {
		termH = 1
	}
	termW := inner

	termContent := tab.renderGrid(termW, termH)

	var sb strings.Builder

	// Top border
	sb.WriteString(bStyle.Render("╭" + strings.Repeat("─", inner) + "╮"))
	sb.WriteString("\n")

	// Content rows — exactly termH lines
	lines := strings.Split(termContent, "\n")
	written := 0
	for _, line := range lines {
		if written >= termH {
			break
		}
		vis := ansi.StringWidth(line)
		if vis > termW {
			line = ansi.Truncate(line, termW, "")
			vis = termW
		}
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString(line)
		if pad := termW - vis; pad > 0 {
			sb.WriteString(strings.Repeat(" ", pad))
		}
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString("\n")
		written++
	}
	for written < termH {
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString(strings.Repeat(" ", termW))
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString("\n")
		written++
	}

	// Bottom border — no trailing newline
	sb.WriteString(bStyle.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return sb.String()
}

// keyToBytes converts a bubbletea key-press name to the terminal byte sequence
// that should be forwarded to the PTY.
func keyToBytes(s string) []byte {
	// ctrl+letter → bytes 1-26
	if strings.HasPrefix(s, "ctrl+") && len(s) == 6 {
		c := s[5]
		if c >= 'a' && c <= 'z' {
			return []byte{c - 'a' + 1}
		}
		switch c {
		case '[':
			return []byte{27}
		case '\\':
			return []byte{28}
		case ']':
			return []byte{29}
		case '^':
			return []byte{30}
		case '_':
			return []byte{31}
		}
	}
	// alt+char → ESC + char
	if strings.HasPrefix(s, "alt+") && len(s) == 5 {
		return []byte{27, s[4]}
	}
	switch s {
	case "enter":
		return []byte{'\r'}
	case "backspace":
		return []byte{127}
	case "tab":
		return []byte{'\t'}
	case "shift+tab":
		return []byte{27, '[', 'Z'}
	case "esc":
		return []byte{27}
	case "space":
		return []byte{' '}
	case "up":
		return []byte{27, '[', 'A'}
	case "down":
		return []byte{27, '[', 'B'}
	case "right":
		return []byte{27, '[', 'C'}
	case "left":
		return []byte{27, '[', 'D'}
	case "home":
		return []byte{27, '[', 'H'}
	case "end":
		return []byte{27, '[', 'F'}
	case "delete":
		return []byte{27, '[', '3', '~'}
	case "pgup":
		return []byte{27, '[', '5', '~'}
	case "pgdown":
		return []byte{27, '[', '6', '~'}
	case "f1":
		return []byte{27, 'O', 'P'}
	case "f2":
		return []byte{27, 'O', 'Q'}
	case "f3":
		return []byte{27, 'O', 'R'}
	case "f4":
		return []byte{27, 'O', 'S'}
	case "f5":
		return []byte{27, '[', '1', '5', '~'}
	case "f6":
		return []byte{27, '[', '1', '7', '~'}
	case "f7":
		return []byte{27, '[', '1', '8', '~'}
	case "f8":
		return []byte{27, '[', '1', '9', '~'}
	case "f9":
		return []byte{27, '[', '2', '0', '~'}
	case "f10":
		return []byte{27, '[', '2', '1', '~'}
	case "f11":
		return []byte{27, '[', '2', '3', '~'}
	case "f12":
		return []byte{27, '[', '2', '4', '~'}
	}
	// Single printable character
	if len(s) == 1 {
		return []byte(s)
	}
	return nil
}
