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
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

// paletteCommand is one entry of the Ctrl+K action list.
type paletteCommand struct {
	Label string
	Keys  string // hint shown on the right
	Run   func(m *AppModel) tea.Cmd
}

// paletteCommands returns the actions offered by the command palette.
//
// The list is ordered by usefulness so the first entry is a sensible default
// when the user opens the palette and hits Enter.
func paletteCommands() []paletteCommand {
	return []paletteCommand{
		{"Connect (ssh, suspends TUI)", "Enter", func(m *AppModel) tea.Cmd {
			s := m.selectedServer()
			if s == nil {
				return nil
			}
			alias := s.Alias
			svc := m.serverService
			return tea.ExecProcess(sshCommand(alias), func(err error) tea.Msg {
				if err != nil {
					return StatusMsg{Text: "SSH error: " + err.Error(), Color: colorErrHex}
				}
				_ = svc.RecordConnect(alias)
				return loadServers(svc, "")()
			})
		}},
		{"Open embedded session", "→", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				return m.openSessionInTab(*s)
			}
			return nil
		}},
		{"Full-screen session view", "o", func(m *AppModel) tea.Cmd {
			if len(m.sessions.tabs) > 0 {
				m.state = StateSessionView
			}
			return nil
		}},
		{"Add server", "a", func(m *AppModel) tea.Cmd {
			m.form = NewFormModel(FormAdd, nil)
			m.state = StateAddForm
			return m.form.Init()
		}},
		{"Edit server", "e", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				clone := *s
				m.form = NewFormModel(FormEdit, &clone)
				m.state = StateEditForm
				return m.form.Init()
			}
			return nil
		}},
		{"Delete server", "d", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				clone := *s
				m.deleteTarget = &clone
				m.state = StateDeleteConfirm
			}
			return nil
		}},
		{"Pin / unpin server", "p", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				pinned := !s.PinnedAt.IsZero()
				if err := m.serverService.SetPinned(s.Alias, !pinned); err != nil {
					var cmd tea.Cmd
					m.statusBar, cmd = m.statusBar.Show("Pin failed: "+err.Error(), colorErrHex)
					return cmd
				}
				return loadServers(m.serverService, m.search.Value())
			}
			return nil
		}},
		{"Ping server", "g", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				return pingServer(m.serverService, *s)
			}
			return nil
		}},
		{"Copy ssh command", "c", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				cmd := BuildSSHCommand(*s)
				var status tea.Cmd
				if err := copyToClipboard(cmd); err != nil {
					m.statusBar, status = m.statusBar.Show("Copy failed: "+err.Error(), colorErrHex)
				} else {
					m.statusBar, status = m.statusBar.Show("Copied: "+cmd, "")
				}
				return status
			}
			return nil
		}},
		{"Open SFTP file browser", "f", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				m.fileBrowser = NewFileBrowserModel(*s, m.serverService, m.width, m.height)
				m.state = StateFileBrowser
				return m.fileBrowser.Init()
			}
			return nil
		}},
		{"Start port forwarding", "F", func(m *AppModel) tea.Cmd {
			s := m.selectedServer()
			if s == nil {
				return nil
			}
			alias := s.Alias
			svc := m.serverService
			rules := svc.ForwardRules(alias)
			if len(rules) == 0 {
				return nil
			}
			return func() tea.Msg {
				_, err := svc.StartForward(alias, rules, nil)
				return ForwardActionMsg{Alias: alias, Started: err == nil, Err: err}
			}
		}},
		{"Stop port forwarding", "x", func(m *AppModel) tea.Cmd {
			if s := m.selectedServer(); s != nil {
				alias := s.Alias
				svc := m.serverService
				return func() tea.Msg {
					return ForwardActionMsg{Alias: alias, Stopped: true, Err: svc.StopForwarding(alias)}
				}
			}
			return nil
		}},
		{"Port forwarding manager", "V", func(m *AppModel) tea.Cmd {
			m.state = StateForwards
			m.forwards = m.serverService.ForwardStatuses()
			return nil
		}},
		{"SSH key manager", "K", func(m *AppModel) tea.Cmd {
			m.state = StateKeys
			m.keys = nil
			m.keysStatus = "loading keys…"
			svc := m.serverService
			return func() tea.Msg {
				keys, err := svc.ListSSHKeys()
				return KeysLoadedMsg{Keys: keys, Err: err}
			}
		}},
		{"known_hosts manager", "H", func(m *AppModel) tea.Cmd {
			m.state = StateKnownHosts
			m.knownHosts = nil
			svc := m.serverService
			return func() tea.Msg {
				entries, err := svc.ListKnownHosts()
				return KnownHostsLoadedMsg{Entries: entries, Err: err}
			}
		}},
		{"Change sort order", "s", func(m *AppModel) tea.Cmd {
			m.sortMode = m.sortMode.Next()
			m.applySort()
			var cmd tea.Cmd
			m.statusBar, cmd = m.statusBar.Show(m.sortMode.String(), "")
			return cmd
		}},
		{"Toggle theme", "t", func(m *AppModel) tea.Cmd {
			m.theme = m.theme.Next()
			applyTheme(m.theme)
			var cmd tea.Cmd
			m.statusBar, cmd = m.statusBar.Show(m.theme.String(), "")
			return cmd
		}},
		{"Search servers", "/", func(m *AppModel) tea.Cmd {
			m.searching = true
			m.search.Focus()
			m.applyLayout()
			return textinput.Blink
		}},
		{"Reload config", "r", func(m *AppModel) tea.Cmd {
			return loadServers(m.serverService, m.search.Value())
		}},
		{"Help", "?", func(m *AppModel) tea.Cmd {
			m.help = NewHelpModel(m.width, m.height)
			m.state = StateHelp
			return nil
		}},
		{"Quit", "q", func(m *AppModel) tea.Cmd { return tea.Quit }},
	}
}

// paletteState is the Ctrl+K overlay's transient model state.
type paletteState struct {
	query    textinput.Model
	filtered []paletteCommand
	cursor   int
}

// newPaletteState builds a focused palette with the full command list.
func newPaletteState() paletteState {
	ti := textinput.New()
	ti.Placeholder = "type a command…"
	ti.CharLimit = 64
	ti.SetWidth(46)
	ti.Focus()
	return paletteState{query: ti, filtered: paletteCommands()}
}

// refilter recomputes the visible entries for the current query.
func (p *paletteState) refilter() {
	all := paletteCommands()
	q := strings.ToLower(strings.TrimSpace(p.query.Value()))
	if q == "" {
		p.filtered = all
	} else {
		p.filtered = p.filtered[:0]
		for _, c := range all {
			if strings.Contains(strings.ToLower(c.Label), q) ||
				strings.Contains(strings.ToLower(c.Keys), q) {
				p.filtered = append(p.filtered, c)
			}
		}
	}
	if p.cursor >= len(p.filtered) {
		p.cursor = len(p.filtered) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// viewPalette renders the command palette overlay.
func (m AppModel) viewPalette() string {
	p := m.palette
	// The selected row style adds one column of padding on each side, so the
	// text budget inside the border is width-4-2.
	const width = 60
	const rowWidth = width - 6

	var sb strings.Builder
	sb.WriteString(PanelTitleStyle.Render("Commands"))
	sb.WriteString("\n")
	sb.WriteString(p.query.View())
	sb.WriteString("\n\n")

	if len(p.filtered) == 0 {
		sb.WriteString(PanelSubtleStyle.Render("no matching command"))
		sb.WriteString("\n")
	}

	const visible = 12
	start := 0
	if p.cursor >= visible {
		start = p.cursor - visible + 1
	}
	end := start + visible
	if end > len(p.filtered) {
		end = len(p.filtered)
	}
	labelW := rowWidth - 6 // leave room for " " + the key hint
	for i := start; i < end; i++ {
		c := p.filtered[i]
		label := ansi.Truncate(c.Label, labelW, "…")
		line := cellPad(fmt.Sprintf("%-*s", labelW, label)+" "+c.Keys, rowWidth)
		if i == p.cursor {
			sb.WriteString(PaletteSelectedStyle.Render(line))
		} else {
			sb.WriteString(PaletteItemStyle.Render(line))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(HelpStyle.Render("Enter: run   Esc: cancel   ↑/↓: select"))

	box := FormBorderStyle.Width(width).Render(sb.String())
	return overlayCenter(m.viewBackground(), box, m.width, m.height)
}

// updatePalette handles input while the palette is open.
func (m AppModel) updatePalette(msg tea.Msg) (tea.Model, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.palette.query, cmd = m.palette.query.Update(msg)
		m.palette.refilter()
		return m, cmd
	}

	switch kp.String() {
	case "esc":
		m.state = StateList
		return m, nil

	case "enter":
		m.state = StateList
		if len(m.palette.filtered) == 0 {
			return m, nil
		}
		chosen := m.palette.filtered[m.palette.cursor]
		if chosen.Run == nil {
			return m, nil
		}
		return m, chosen.Run(&m)

	case "down", "ctrl+n":
		if m.palette.cursor < len(m.palette.filtered)-1 {
			m.palette.cursor++
		}
		return m, nil

	case "up", "ctrl+p":
		if m.palette.cursor > 0 {
			m.palette.cursor--
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.palette.query, cmd = m.palette.query.Update(msg)
	m.palette.refilter()
	return m, cmd
}

// sshCommand builds the os/exec command used by the palette's connect action.
func sshCommand(alias string) *exec.Cmd {
	// #nosec G204 -- alias is validated by the service layer.
	return exec.Command("ssh", alias)
}

// copyToClipboard is a thin indirection so the palette and the list share one
// clipboard implementation.
func copyToClipboard(text string) error {
	return clipboard.WriteAll(text)
}
