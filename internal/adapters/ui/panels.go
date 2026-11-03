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
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/linpr/lazyssh/internal/core/domain"
)

// ---- messages ----

// KeysLoadedMsg carries the result of ListSSHKeys.
type KeysLoadedMsg struct {
	Keys []domain.SSHKey
	Err  error
}

// KnownHostsLoadedMsg carries the result of ListKnownHosts.
type KnownHostsLoadedMsg struct {
	Entries []domain.KnownHostEntry
	Err     error
}

// ForwardActionMsg reports the outcome of a start/stop request.
type ForwardActionMsg struct {
	Alias   string
	Started bool
	Stopped bool
	Err     error
}

// ---- generic list panel rendering ----

// panelSpec describes a simple vertical list overlay (forwards, keys, hosts).
type panelSpec struct {
	title   string
	hint    string
	columns []string
	rows    [][]string
	cursor  int
	empty   string
	width   int
	height  int
}

// renderPanel draws a bordered list overlay centered over base.
func renderPanel(spec panelSpec, base string, w, h int) string {
	inner := w - 10
	if inner < 40 {
		inner = 40
	}
	if inner > 120 {
		inner = 120
	}
	visible := h - 8
	if visible < 3 {
		visible = 3
	}

	var sb strings.Builder
	sb.WriteString(PanelTitleStyle.Render(spec.title))
	sb.WriteString("\n")
	sb.WriteString(PanelSubtleStyle.Render(spec.hint))
	sb.WriteString("\n\n")

	// Column widths are derived from the header + every row so the table stays
	// aligned instead of each cell padding itself independently.
	widths := make([]int, len(spec.columns))
	for i, col := range spec.columns {
		widths[i] = ansi.StringWidth(col)
	}
	for _, row := range spec.rows {
		for i, c := range row {
			if i < len(widths) {
				if w := ansi.StringWidth(c); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}

	formatRow := func(cells []string, last string) string {
		var parts []string
		for i, c := range cells {
			if i == len(cells)-1 {
				parts = append(parts, cellPad(c, widths[i]))
				continue
			}
			parts = append(parts, cellPad(c, widths[i]))
		}
		if last != "" {
			parts = append(parts, last)
		}
		return strings.Join(parts, "  ")
	}

	if len(spec.columns) > 0 {
		sb.WriteString(PanelSubtleStyle.Render(formatRow(spec.columns, "")))
		sb.WriteString("\n")
	}

	if len(spec.rows) == 0 {
		sb.WriteString(PanelSubtleStyle.Render(spec.empty))
		sb.WriteString("\n")
	}

	start := 0
	if spec.cursor >= visible {
		start = spec.cursor - visible + 1
	}
	end := start + visible
	if end > len(spec.rows) {
		end = len(spec.rows)
	}
	for i := start; i < end; i++ {
		line := formatRow(spec.rows[i], "")
		line = ansi.Truncate(line, inner, "…")
		if i == spec.cursor {
			sb.WriteString(PanelHighlightStyle.Render(" " + cellPad(line, inner) + " "))
		} else {
			sb.WriteString(" " + line)
		}
		sb.WriteString("\n")
	}

	box := FormBorderStyle.Width(inner + 6).Render(sb.String())
	return overlayCenter(base, box, w, h)
}

// ---- forwards panel ----

func (m AppModel) viewForwards() string {
	spec := panelSpec{
		title:   "Port Forwarding",
		hint:    "j/k: move   x: stop   r: refresh   R: stop all   Esc: back",
		columns: []string{"ALIAS", "FORWARDS", "PID", "UPTIME", "STATE"},
		cursor:  m.forwardsCur,
		empty:   "No forwarding processes are running.",
	}
	for _, f := range m.forwards {
		state := "stopped"
		if f.Running {
			state = "running"
		}
		spec.rows = append(spec.rows, []string{
			f.Alias,
			joinRules(f.Rules),
			fmt.Sprintf("%d", f.PID),
			f.Duration().Truncate(time.Second).String(),
			state,
		})
	}
	return renderPanel(spec, m.viewBackground(), m.width, m.height)
}

// joinRules renders up to two rule specs for the table cell.
func joinRules(rules []domain.ForwardRule) string {
	var parts []string
	for i, r := range rules {
		if i == 2 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, string(r.Kind)+" "+r.Spec)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func (m AppModel) updateForwards(msg tea.Msg) (tea.Model, tea.Cmd) {
	if kp, ok := msg.(tea.KeyPressMsg); ok {
		svc := m.serverService
		switch kp.String() {
		case "esc", "q":
			m.state = StateList
		case "r":
			m.forwards = svc.ForwardStatuses()
		case "j", "down":
			if m.forwardsCur < len(m.forwards)-1 {
				m.forwardsCur++
			}
		case "k", "up":
			if m.forwardsCur > 0 {
				m.forwardsCur--
			}
		case "x":
			if len(m.forwards) > 0 && m.forwardsCur < len(m.forwards) {
				alias := m.forwards[m.forwardsCur].Alias
				return m, func() tea.Msg {
					return ForwardActionMsg{Alias: alias, Stopped: true, Err: svc.StopForwarding(alias)}
				}
			}
		case "R":
			aliases := make([]string, 0, len(m.forwards))
			for _, f := range m.forwards {
				aliases = append(aliases, f.Alias)
			}
			return m, func() tea.Msg {
				var errs []string
				for _, a := range aliases {
					if err := svc.StopForwarding(a); err != nil {
						errs = append(errs, a+": "+err.Error())
					}
				}
				if len(errs) > 0 {
					return StatusMsg{Text: "stop all: " + strings.Join(errs, "; "), Color: colorErrHex}
				}
				return StatusMsg{Text: fmt.Sprintf("stopped %d forward(s)", len(aliases)), Color: colorOKHex}
			}
		}
	}
	return m, nil
}

// ---- keys panel ----

func (m AppModel) viewKeys() string {
	spec := panelSpec{
		title:   "SSH Keys",
		hint:    "j/k: move   a: add to agent   g: generate   y: copy to host   r: reload   Esc: back",
		columns: []string{"KEY", "TYPE", "BITS", "AGENT", "PASSPHRASE"},
		cursor:  m.keysCur,
		empty:   m.keysStatus,
	}
	if len(m.keys) > 0 && spec.empty == "" {
		spec.empty = "No keys."
	}
	for _, k := range m.keys {
		agent := "no"
		if k.LoadedInAgent {
			agent = "yes"
		}
		protected := "no"
		if k.Protected {
			protected = "yes"
		}
		name := k.Path
		if home, err := homeDir(); err == nil {
			name = strings.Replace(name, home, "~", 1)
		}
		typ := k.Type
		if typ == "" {
			typ = "?"
		}
		spec.rows = append(spec.rows, []string{
			name,
			typ,
			strconv.Itoa(k.Bits),
			agent,
			protected,
		})
	}
	return renderPanel(spec, m.viewBackground(), m.width, m.height)
}

func (m AppModel) updateKeys(msg tea.Msg) (tea.Model, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	svc := m.serverService
	switch kp.String() {
	case "esc", "q":
		m.state = StateList
	case "r":
		m.keys = nil
		m.keysStatus = "loading keys…"
		return m, func() tea.Msg {
			keys, err := svc.ListSSHKeys()
			return KeysLoadedMsg{Keys: keys, Err: err}
		}
	case "j", "down":
		if m.keysCur < len(m.keys)-1 {
			m.keysCur++
		}
	case "k", "up":
		if m.keysCur > 0 {
			m.keysCur--
		}
	case "g":
		// Ask for a destination path, then generate with sensible defaults.
		label := "New key path (e.g. ~/.ssh/id_ed25519_work)"
		def := "~/.ssh/id_" + time.Now().Format("20060102")
		m.promptLabel, m.promptValue, m.promptAction, m.promptTarget = newPrompt(label, def, promptGenKeyPath, "")
		m.state = StateTextPrompt
		return m, m.promptValue.Focus()

	case "a":
		if len(m.keys) > 0 && m.keysCur < len(m.keys) {
			path := m.keys[m.keysCur].Path
			return m, func() tea.Msg {
				if err := svc.AddKeyToAgent(path); err != nil {
					return StatusMsg{Text: "ssh-add: " + err.Error(), Color: colorErrHex}
				}
				return StatusMsg{Text: "added to agent: " + path, Color: colorOKHex}
			}
		}
	case "y":
		if len(m.keys) > 0 && m.keysCur < len(m.keys) {
			path := m.keys[m.keysCur].Path
			if s := m.selectedServer(); s != nil {
				alias := s.Alias
				return m, func() tea.Msg {
					if err := svc.CopyKeyToHost(path, alias); err != nil {
						return StatusMsg{Text: "ssh-copy-id: " + err.Error(), Color: colorErrHex}
					}
					return StatusMsg{Text: "key installed on " + alias, Color: colorOKHex}
				}
			}
		}
	}
	return m, nil
}

// ---- known_hosts panel ----

func (m AppModel) viewKnownHosts() string {
	spec := panelSpec{
		title:   "known_hosts",
		hint:    "j/k: move   d: remove host   r: reload   Esc: back",
		columns: []string{"HOST", "TYPE", "HASHED"},
		cursor:  m.knownHostsCur,
		empty:   "known_hosts is empty.",
	}
	if m.knownHostsMsg != "" {
		spec.empty = m.knownHostsMsg
	}
	for _, e := range m.knownHosts {
		host := e.Hosts
		if len(host) > 48 {
			host = host[:45] + "…"
		}
		hashed := "no"
		if e.Hashed {
			hashed = "yes"
		}
		spec.rows = append(spec.rows, []string{
			host,
			e.KeyType,
			hashed,
		})
	}
	return renderPanel(spec, m.viewBackground(), m.width, m.height)
}

func (m AppModel) updateKnownHosts(msg tea.Msg) (tea.Model, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	svc := m.serverService
	switch kp.String() {
	case "esc", "q":
		m.state = StateList
	case "r":
		m.knownHosts = nil
		m.knownHostsMsg = ""
		return m, func() tea.Msg {
			entries, err := svc.ListKnownHosts()
			return KnownHostsLoadedMsg{Entries: entries, Err: err}
		}
	case "j", "down":
		if m.knownHostsCur < len(m.knownHosts)-1 {
			m.knownHostsCur++
		}
	case "k", "up":
		if m.knownHostsCur > 0 {
			m.knownHostsCur--
		}
	case "d":
		if len(m.knownHosts) > 0 && m.knownHostsCur < len(m.knownHosts) {
			host := m.knownHosts[m.knownHostsCur].Hosts
			if strings.HasPrefix(host, "|1|") {
				m.knownHostsMsg = "hashed entry: remove it with ssh-keygen -R <hostname>"
				return m, nil
			}
			if i := strings.Index(host, ","); i > 0 {
				host = host[:i]
			}
			return m, func() tea.Msg {
				n, err := svc.RemoveKnownHost(host)
				if err != nil {
					return StatusMsg{Text: "known_hosts: " + err.Error(), Color: colorErrHex}
				}
				return StatusMsg{Text: fmt.Sprintf("removed %d entry for %s", n, host), Color: colorOKHex}
			}
		}
	}
	return m, nil
}

// ---- text prompt ----

// newPrompt builds a focused single-line input.
func newPrompt(label, value, action, target string) (string, textinput.Model, string, string) {
	ti := textinput.New()
	ti.Placeholder = "type and press Enter"
	ti.CharLimit = 256
	ti.SetWidth(48)
	ti.SetValue(value)
	ti.Focus()
	return label, ti, action, target
}

func (m AppModel) viewPrompt() string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		KeyStyle.Render(m.promptLabel),
		"",
		m.promptValue.View(),
		"",
		HelpStyle.Render("Enter: confirm   Esc: cancel"),
	)
	return overlayCenter(m.viewBackground(), FormBorderStyle.Width(60).Render(content), m.width, m.height)
}

func (m AppModel) updatePrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		m.promptValue, cmd = m.promptValue.Update(msg)
		return m, cmd
	}
	switch kp.String() {
	case "esc":
		m.state = StateList
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.promptValue.Value())
		m.state = StateList
		return m, m.promptSubmit(m.promptAction, m.promptTarget, value)
	}
	var cmd tea.Cmd
	m.promptValue, cmd = m.promptValue.Update(msg)
	return m, cmd
}
