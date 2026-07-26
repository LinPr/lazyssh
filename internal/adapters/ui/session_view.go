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
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/hinshun/vt10x"
	"charm.land/lipgloss/v2"
)

// Unexported attribute bitmask values from vt10x/state.go (mirrored here).
const (
	vtAttrReverse   int16 = 1 << 0 // 1
	vtAttrUnderline int16 = 1 << 1 // 2
	vtAttrBold      int16 = 1 << 2 // 4
	vtAttrItalic    int16 = 1 << 4 // 16
)

// renderSessionView renders the full session panel: left list + right terminal.
func (m AppModel) renderSessionView() string {
	bodyH := m.height - 1 // 1 row for status bar
	if bodyH < 3 {
		bodyH = 3
	}

	left := m.sessions.renderList(bodyH)
	right := m.sessions.renderTerminal(m.width-sessionListWidth, bodyH)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	statusBar := m.statusBar.View(m.width)
	return lipgloss.JoinVertical(lipgloss.Left, panels, statusBar)
}

// renderList draws the left panel: a bordered list of sessions with status emoji.
func (m SessionsModel) renderList(h int) string {
	bStyle := lipgloss.NewStyle().Foreground(colorBorder)
	inner := sessionListWidth - 2 // width inside │ borders

	writeRow := func(sb *strings.Builder, content string) {
		vis := ansi.StringWidth(content)
		if vis > inner {
			content = ansi.Truncate(content, inner, "")
			vis = inner
		}
		if pad := inner - vis; pad > 0 {
			content += strings.Repeat(" ", pad)
		}
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString(content)
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString("\n")
	}

	var sb strings.Builder

	// Top border + title
	sb.WriteString(bStyle.Render("╭" + strings.Repeat("─", inner) + "╮"))
	sb.WriteString("\n")

	titleText := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("Sessions")
	titlePad := inner - ansi.StringWidth("Sessions")
	if titlePad < 0 {
		titlePad = 0
	}
	writeRow(&sb, titleText+strings.Repeat(" ", titlePad))

	// Session rows
	shown := 1
	for i, t := range m.tabs {
		if shown >= h-2 { // -2 for top+bottom border
			break
		}
		emoji := "🟢"
		if t.closed {
			emoji = "🔴"
		}
		style := lipgloss.NewStyle().Foreground(colorText)
		if i == m.active {
			style = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorText).
				Background(colorSelected)
		}
		// emoji is 2 wide, space, alias
		label := emoji + " " + t.server.Alias
		writeRow(&sb, style.Render(label))
		shown++
	}

	// Fill blank rows
	for shown < h-1 {
		writeRow(&sb, "")
		shown++
	}

	// Hint row at bottom (1 row before border)
	hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("^A:menu q:list")
	writeRow(&sb, hint)

	// Bottom border — no trailing newline so JoinHorizontal aligns correctly
	sb.WriteString(bStyle.Render("╰" + strings.Repeat("─", inner) + "╯"))

	return sb.String()
}

// renderTerminal draws the right panel containing the active session's terminal output.
func (m SessionsModel) renderTerminal(w, h int) string {
	tab := m.activeTab()
	if tab == nil {
		return renderEmptyTerminal(w, h)
	}
	if tab.closed {
		return renderClosedTerminal(w, h, tab.server.Alias)
	}

	tab.term.Lock()
	defer tab.term.Unlock()

	return renderVT10xGrid(tab.term, w, h)
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

// renderVT10xGrid converts the vt10x terminal cell grid into a lipgloss-styled
// string. Consecutive cells sharing the same attributes are batched to minimise
// style transitions.
func renderVT10xGrid(term vt10x.View, w, h int) string {
	cols, rows := term.Size()
	cur := term.Cursor()

	var out strings.Builder
	out.Grow(rows * (cols + 20))

	for row := 0; row < rows && row < h; row++ {
		if row > 0 {
			out.WriteByte('\n')
		}

		// Batch cells with identical style attributes.
		type span struct {
			text string
			fg   vt10x.Color
			bg   vt10x.Color
			mode int16
		}

		var spans []span
		var cur_span span

		for col := 0; col < cols && col < w; col++ {
			g := term.Cell(col, row)
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			// Show block cursor on active position
			if col == cur.X && row == cur.Y {
				g.Mode |= vtAttrReverse
			}

			if col == 0 {
				cur_span = span{string(ch), g.FG, g.BG, g.Mode}
			} else if g.FG == cur_span.fg && g.BG == cur_span.bg && g.Mode == cur_span.mode {
				cur_span.text += string(ch)
			} else {
				spans = append(spans, cur_span)
				cur_span = span{string(ch), g.FG, g.BG, g.Mode}
			}
		}
		if cols > 0 {
			spans = append(spans, cur_span)
		}

		for _, sp := range spans {
			style := glyphStyle(sp.fg, sp.bg, sp.mode)
			out.WriteString(style.Render(sp.text))
		}
	}

	// Pad missing rows
	for row := rows; row < h; row++ {
		out.WriteByte('\n')
		out.WriteString(strings.Repeat(" ", w))
	}

	return out.String()
}

// glyphStyle builds a lipgloss.Style for the given vt10x fg/bg/mode attributes.
func glyphStyle(fg, bg vt10x.Color, mode int16) lipgloss.Style {
	s := lipgloss.NewStyle()

	if fg != vt10x.DefaultFG {
		s = s.Foreground(vt10xColor(fg))
	}
	if bg != vt10x.DefaultBG {
		s = s.Background(vt10xColor(bg))
	}
	if mode&vtAttrReverse != 0 {
		s = s.Reverse(true)
	}
	if mode&vtAttrBold != 0 {
		s = s.Bold(true)
	}
	if mode&vtAttrUnderline != 0 {
		s = s.Underline(true)
	}
	if mode&vtAttrItalic != 0 {
		s = s.Italic(true)
	}
	return s
}

// vt10xColor converts a vt10x.Color to a color.Color that lipgloss can consume.
//
//   - 0–255  : ANSI indexed color → lipgloss.Color("N")
//   - 256 – (1<<24-1): 24-bit true color r<<16|g<<8|b → lipgloss.Color("#rrggbb")
//   - ≥ 1<<24: DefaultFG/DefaultBG — caller must guard against these.
func vt10xColor(c vt10x.Color) color.Color {
	if c < 256 {
		return lipgloss.Color(fmt.Sprintf("%d", c))
	}
	// 24-bit true color encoded as r<<16|g<<8|b
	r := (c >> 16) & 0xFF
	g := (c >> 8) & 0xFF
	b := c & 0xFF
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
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

	tab.term.Lock()
	termContent := renderVT10xGrid(tab.term, termW, termH)
	tab.term.Unlock()

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
