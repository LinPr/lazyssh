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
	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/mattn/go-runewidth"
)

// IsForwarding is an optional hook supplied by TUI to indicate active forwarding per alias.
var IsForwarding func(alias string) bool

// ServerItem implements list.Item interface for displaying server list items.
type ServerItem struct {
	Server     domain.Server
	PingStatus *bool // nil=unknown, true=up, false=down
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

// Render draws a single server row in minimal format: pingDot + pin + alias.
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

	statusDot := renderPingDot(serverItem.PingStatus)

	var pinStr string
	if !s.PinnedAt.IsZero() {
		pinStr = PinnedStyle.Render("★")
	} else {
		pinStr = " "
	}

	// prefix: "● ★ " = 4 display cols
	prefixCols := 4
	aliasWidth := available - prefixCols
	if aliasWidth < 4 {
		aliasWidth = 4
	}

	alias := truncatePad(s.Alias, aliasWidth)

	line := fmt.Sprintf("%s %s %s", statusDot, pinStr, alias)

	if isSelected {
		line = ListSelectedItemStyle.Width(available).Render(line)
	} else {
		line = ListItemStyle.Render(line)
	}

	fmt.Fprint(w, line)
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
// Strings longer than width are truncated and suffixed with "...".
func truncatePad(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w > width {
		t := runewidth.Truncate(s, width, "...")
		tw := runewidth.StringWidth(t)
		if tw < width {
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
	w := runewidth.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}
