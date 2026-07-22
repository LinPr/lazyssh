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
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// StatusExpiredMsg is sent when the status bar message has expired
type StatusExpiredMsg struct{}

// StatusBar displays temporary messages at the bottom of the screen.
// Messages auto-clear after 2 seconds.
type StatusBar struct {
	msg     string
	color   string // "" = default green, "#FF6B6B" = red, etc.
	expires time.Time
}

// Show sets a new status message and schedules auto-expiry after 2 seconds.
// color: "" = green (success), "#FF6B6B" = red (error).
func (s StatusBar) Show(msg, color string) (StatusBar, tea.Cmd) {
	s.msg = msg
	s.color = color
	s.expires = time.Now().Add(2 * time.Second)

	cmd := tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return StatusExpiredMsg{}
	})

	return s, cmd
}

// Update handles incoming messages, in particular StatusExpiredMsg.
func (s StatusBar) Update(msg tea.Msg) (StatusBar, tea.Cmd) {
	switch msg.(type) {
	case StatusExpiredMsg:
		// Only clear if the message has actually expired (guards against
		// stale ticks from a previous Show call)
		if !s.expires.IsZero() && time.Now().After(s.expires) {
			s.msg = ""
			s.color = ""
			s.expires = time.Time{}
		}
	}
	return s, nil
}

// View renders the status bar at the given width.
// When no message is active it renders the default help line.
func (s StatusBar) View(width int) string {
	base := lipgloss.NewStyle().
		Background(colorBgAlt).
		Width(width)

	if s.msg != "" && (s.expires.IsZero() || time.Now().Before(s.expires)) {
		// Determine foreground color for the message
		fg := colorGreen // bright green (success)
		if s.color != "" {
			fg = lipgloss.Color(s.color)
		}

		msgStyle := base.
			Foreground(fg).
			Bold(true).
			Align(lipgloss.Center)

		return msgStyle.Render(s.msg)
	}

	// No active message — hide the status bar.
	return ""
}
