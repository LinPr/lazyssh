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
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/linpr/lazyssh/internal/core/domain"
)

// IsForwarding is an optional hook supplied by TUI to indicate active forwarding per alias.
var IsForwarding func(alias string) bool

// ServerItem implements list.Item interface for displaying server list items.
type ServerItem struct {
	Server        domain.Server
	PingStatus    *bool // nil=unknown, true=up, false=down
	SessionActive bool  // true when a live PTY session exists for this server
	Forwarding    bool  // true when a port-forward process is running
}

func (i ServerItem) Title() string {
	return i.Server.Alias
}

func (i ServerItem) Description() string {
	return i.Server.Host
}

func (i ServerItem) FilterValue() string {
	tags := strings.Join(i.Server.Tags, " ")
	return fmt.Sprintf("%s %s %s %s", i.Server.Alias, i.Server.Host, i.Server.User, tags)
}

// ServerDelegate implements list.ItemDelegate for custom rendering.
type ServerDelegate struct{}

func NewServerDelegate() ServerDelegate {
	return ServerDelegate{}
}

func (d ServerDelegate) Height() int {
	return 1
}

func (d ServerDelegate) Spacing() int {
	return 0
}

func (d ServerDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd {
	return nil
}

// badgeWidth is the display width reserved after the dot for the pin marker.
const pinColumnWidth = 2

// Render draws a single server row: status dot + pin + alias + badges.
//
// Two details matter here:
//
//   - All padding is derived from *visible* widths. Measuring a string that
//     already contains ANSI escapes counted every escape byte, which reserved 21
//     columns for a 1-column dot and truncated every alias to "s...".
//   - The selected row is composed from plain text and highlighted with a single
//     style. Nesting styled segments inside another style does not work: each
//     inner Render() ends with a full reset, which clears the selection
//     background for the rest of the line.
func (d ServerDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	serverItem, ok := item.(ServerItem)
	if !ok {
		return
	}

	s := serverItem.Server
	isSelected := index == m.Index()

	available := m.Width()
	if available <= 0 {
		available = 40
	}

	// Trailing badges are optional; drop them first on narrow panes.
	var badges []string
	if serverItem.Forwarding {
		badges = append(badges, "⇅")
	}
	if serverItem.SessionActive {
		badges = append(badges, "●")
	}
	badgeText := ""
	if len(badges) > 0 {
		badgeText = " " + strings.Join(badges, " ")
	}
	badgeCols := ansi.StringWidth(badgeText)
	if badgeCols+12 > available {
		badgeText = ""
		badgeCols = 0
	}

	pin := " "
	if !s.PinnedAt.IsZero() {
		pin = "★"
	}

	// dot(1) + space + pin(1) + space = 4 columns before the alias.
	const prefixCols = 4
	aliasWidth := available - prefixCols - badgeCols
	if aliasWidth < 3 {
		aliasWidth = 3
	}
	alias := truncatePad(s.Alias, aliasWidth)

	if isSelected {
		row := "●" + " " + pin + " " + alias + badgeText
		if pad := available - ansi.StringWidth(row); pad > 0 {
			row += strings.Repeat(" ", pad)
		}
		fmt.Fprint(w, ListSelectedItemStyle.Render(row))
		return
	}

	line := renderPingDot(serverItem.PingStatus) + " " + pinStyled(s) + " " + alias + badgeText
	if pad := available - ansi.StringWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	fmt.Fprint(w, ListItemStyle.Render(line))
}

// pinStyled renders the pin marker with its own colour.
func pinStyled(s domain.Server) string {
	if s.PinnedAt.IsZero() {
		return " "
	}
	return PinnedStyle.Render("★")
}

// renderPingDot returns a styled dot reflecting ping status.
func renderPingDot(status *bool) string {
	if status == nil {
		return PingUnknownStyle.Render("●")
	}
	if *status {
		return PingOnlineStyle.Render("●")
	}
	return PingOfflineStyle.Render("●")
}

// truncatePad returns s occupying exactly `width` display columns.
// Strings longer than width are truncated and suffixed with "…".
func truncatePad(s string, width int) string {
	w := ansi.StringWidth(s)
	if w > width {
		t := ansi.Truncate(s, width, "…")
		if tw := ansi.StringWidth(t); tw < width {
			return t + strings.Repeat(" ", width-tw)
		}
		return t
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// cellPad pads a string so its display width is at least `width` cells.
func cellPad(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}
