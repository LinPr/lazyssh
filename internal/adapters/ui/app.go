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
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/linpr/lazyssh/internal/core/ports"
)

type AppState int

const (
	StateList AppState = iota
	StateAddForm
	StateEditForm
	StateDeleteConfirm
	StateHelp
	StateQuickPreview
	StateFormPreview
	StateFileBrowser
	StateSSHAuthInput
	StateSessionView // embedded PTY tab sessions
)

type AppModel struct {
	state         AppState
	serverService ports.ServerService
	servers       []domain.Server
	list          list.Model
	search        textinput.Model
	details       DetailsModel
	form          FormModel
	formPreview   FormSavePreviewMsg
	help          HelpModel
	statusBar     StatusBar
	deleteTarget  *domain.Server
	previewText   string
	pingNotice    string
	sortMode      int
	searching     bool
	width, height int
	version       string
	commit        string
	pingStatus    map[string]*bool
	fileBrowser   FileBrowserModel
	sshAuthInput  textinput.Model
	sshAuthAlias  string
	sshAuthServer domain.Server
	sessions           SessionsModel
	activeSessionAlias string // alias shown in the right-panel overlay ("" = show details)
	terminalFocused    bool   // true = keys forwarded to PTY; false = left panel navigable
}

// Message types
type ServersLoadedMsg []domain.Server
type ServerPingResultMsg struct {
	Alias    string
	Up       bool
	Duration time.Duration
	Err      error
}
type PingStatusMsg struct {
	Alias string
	Up    bool
}
type StatusMsg struct {
	Text  string
	Color string
}

// SSHMasterReadyMsg is sent after a ControlMaster connection is established
// successfully, signalling that the file browser can open.
type SSHMasterReadyMsg struct{ Server domain.Server }

// SSHAuthInputMsg is sent when batch-mode auth fails and the user must provide
// a password or OTP via the TUI overlay.
type SSHAuthInputMsg struct{}

// SSHMasterFailedMsg is sent when EstablishSSHMasterWithPassword returns an error.
type SSHMasterFailedMsg struct {
	Alias string
	Err   error
}

// CloseSSHMasterMsg is sent when the file browser exits so the ControlMaster
// socket is cleaned up in the background.
type CloseSSHMasterMsg struct{ Alias string }

func NewApp(ss ports.ServerService, version, commit string) *AppModel {
	// Initialize search input
	search := textinput.New()
	search.Placeholder = "Type to search..."
	search.CharLimit = 100

	// Initialize list
	delegate := NewServerDelegate()
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = "SSH Servers"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = ListTitleStyle
	l.SetShowHelp(false)

	// Register the IsForwarding hook
	IsForwarding = ss.IsForwarding

	return &AppModel{
		state:         StateList,
		serverService: ss,
		list:          l,
		search:        search,
		details:       NewDetailsModel(),
		statusBar:     StatusBar{},
		version:       version,
		commit:        commit,
	}
}

func NewProgram(ss ports.ServerService, version, commit string) *tea.Program {
	m := NewApp(ss, version, commit)
	return tea.NewProgram(m)
}

// Init loads the initial server list.
func (m AppModel) Init() tea.Cmd {
	return loadServers(m.serverService, "")
}

func loadServers(ss ports.ServerService, query string) tea.Cmd {
	return func() tea.Msg {
		servers, err := ss.ListServers(query)
		if err != nil {
			return StatusMsg{Text: "Failed to load servers: " + err.Error(), Color: "#f7768e"}
		}
		return ServersLoadedMsg(servers)
	}
}

func pingServer(ss ports.ServerService, server domain.Server) tea.Cmd {
	return func() tea.Msg {
		up, dur, err := ss.Ping(server)
		return ServerPingResultMsg{Alias: server.Alias, Up: up, Duration: dur, Err: err}
	}
}

// pingAllServers returns a tea.Batch that pings every server concurrently,
// each delivering a PingStatusMsg when done.
func pingAllServers(ss ports.ServerService, servers []domain.Server) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(servers))
	for _, s := range servers {
		s := s // capture loop variable
		cmds = append(cmds, func() tea.Msg {
			up, _, _ := ss.Ping(s)
			return PingStatusMsg{Alias: s.Alias, Up: up}
		})
	}
	return tea.Batch(cmds...)
}

// selectedServer returns the currently selected server, or nil.
func (m *AppModel) selectedServer() *domain.Server {
	if len(m.servers) == 0 {
		return nil
	}
	idx := m.list.Index()
	if idx < 0 || idx >= len(m.servers) {
		return nil
	}
	return &m.servers[idx]
}

// updateDetailsForSelected loads config for the selected server and auto-switches
// the right panel to show a session overlay when that server has an active session.
func (m *AppModel) updateDetailsForSelected() {
	s := m.selectedServer()
	if s == nil {
		m.details.SetRawConfig("")
		if !m.terminalFocused {
			m.activeSessionAlias = ""
		}
		return
	}
	// When left panel has focus, auto-show session overlay for servers that
	// have an active session, or fall back to config details.
	if !m.terminalFocused {
		if m.sessions.findByAlias(s.Alias) != nil {
			m.activeSessionAlias = s.Alias
		} else {
			m.activeSessionAlias = ""
		}
	}
	raw, err := m.serverService.GetRawConfigBlock(s.Alias)
	if err != nil || raw == "" {
		m.details.SetRawConfig("# " + s.Alias + "\n# (config block not found)")
		return
	}
	m.details.SetRawConfig(raw)
}

// updateListItems rebuilds list items from m.servers, injecting current ping status.
func (m *AppModel) updateListItems() {
	items := make([]list.Item, len(m.servers))
	for i, s := range m.servers {
		items[i] = ServerItem{
			Server:        s,
			PingStatus:    m.pingStatus[s.Alias],
			SessionActive: m.sessions.findByAlias(s.Alias) != nil,
		}
	}
	m.list.SetItems(items)
	m.updateDetailsForSelected()
}

// Update handles all incoming messages.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sessions.width = msg.Width
		m.sessions.height = msg.Height
		m.sessions.resizeAll()

	case ServersLoadedMsg:
		m.servers = []domain.Server(msg)
		m.updateListItems()
		cmds = append(cmds, pingAllServers(m.serverService, m.servers))

	case PingStatusMsg:
		if m.pingStatus == nil {
			m.pingStatus = make(map[string]*bool)
		}
		up := msg.Up
		m.pingStatus[msg.Alias] = &up
		m.updateListItems()

	case StatusMsg:
		var cmd tea.Cmd
		m.statusBar, cmd = m.statusBar.Show(msg.Text, msg.Color)
		cmds = append(cmds, cmd)

	case StatusExpiredMsg:
		var cmd tea.Cmd
		m.statusBar, cmd = m.statusBar.Update(msg)
		cmds = append(cmds, cmd)

	case ServerPingResultMsg:
		var cmd tea.Cmd
		if msg.Err != nil {
			notice := fmt.Sprintf("Ping %s: error: %v", msg.Alias, msg.Err)
			m.statusBar, cmd = m.statusBar.Show(notice, "#f7768e")
			m.pingNotice = notice
		} else if msg.Up {
			notice := fmt.Sprintf("Ping %s: up (%s)", msg.Alias, msg.Duration.Truncate(time.Millisecond))
			m.statusBar, cmd = m.statusBar.Show(notice, "#9ece6a")
			m.pingNotice = notice
		} else {
			notice := fmt.Sprintf("Ping %s: down", msg.Alias)
			m.statusBar, cmd = m.statusBar.Show(notice, "#e0af68")
			m.pingNotice = notice
		}
		cmds = append(cmds, cmd)

	case FormSaveMsg:
		cmds = append(cmds, m.handleFormSave(msg))
		m.state = StateList

	case FormSavePreviewMsg:
		m.formPreview = msg
		m.state = StateFormPreview

	case FormCancelMsg:
		m.state = StateList

	case CloseOverlayMsg:
		m.state = StateList

	case FileBrowserExitMsg:
		m.state = StateList

	case SSHMasterReadyMsg:
		m.fileBrowser = NewFileBrowserModel(msg.Server, m.serverService, m.width, m.height)
		m.state = StateFileBrowser
		return m, tea.Batch(append(cmds, m.fileBrowser.Init())...)

	case CloseSSHMasterMsg:
		go m.serverService.CloseSSHMaster(msg.Alias) //nolint:errcheck

	case SSHAuthInputMsg:
		if s := m.selectedServer(); s != nil {
			m.sshAuthAlias = s.Alias
			m.sshAuthServer = *s
		}
		m.sshAuthInput = textinput.New()
		m.sshAuthInput.Placeholder = "Password / OTP / Verification code"
		m.sshAuthInput.EchoMode = textinput.EchoPassword
		m.sshAuthInput.CharLimit = 128
		m.sshAuthInput.SetWidth(50)
		m.state = StateSSHAuthInput
		return m, m.sshAuthInput.Focus()

	case SSHMasterFailedMsg:
		var cmd tea.Cmd
		m.statusBar, cmd = m.statusBar.Show("SSH auth failed for "+msg.Alias+": "+msg.Err.Error(), "#C62828")
		return m, cmd

	case PtyOutputMsg:
		// PTY has new output — re-render happens automatically; re-queue reader.
		if tab := m.sessions.findByAlias(msg.Alias); tab != nil {
			m.updateListItems() // refresh 🟢 indicators
			return m, waitForPtyOutput(tab)
		}

	case SessionClosedMsg:
		m.sessions.removeByAlias(msg.Alias)
		if m.activeSessionAlias == msg.Alias {
			m.activeSessionAlias = ""
		}
		m.updateListItems()
		var cmd tea.Cmd
		m.statusBar, cmd = m.statusBar.Show("Session closed: "+msg.Alias, "#e0af68")
		if len(m.sessions.tabs) == 0 {
			m.state = StateList
		}
		cmds = append(cmds, cmd)
	}

	switch m.state {
	case StateList:
		model, cmd := m.updateStateList(msg)
		return model, tea.Batch(append(cmds, cmd)...)

	case StateAddForm, StateEditForm:
		newForm, cmd := m.form.Update(msg)
		m.form = newForm
		return m, tea.Batch(append(cmds, cmd)...)

	case StateDeleteConfirm:
		model, cmd := m.updateStateDelete(msg)
		return model, tea.Batch(append(cmds, cmd)...)

	case StateHelp:
		newHelp, cmd := m.help.Update(msg)
		m.help = newHelp
		return m, tea.Batch(append(cmds, cmd)...)

	case StateQuickPreview:
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			_ = msg
			m.state = StateList
		}
		return m, tea.Batch(cmds...)

	case StateFormPreview:
		model, cmd := m.updateStateFormPreview(msg)
		return model, tea.Batch(append(cmds, cmd)...)

	case StateFileBrowser:
		newFB, cmd := m.fileBrowser.Update(msg)
		m.fileBrowser = newFB
		return m, tea.Batch(append(cmds, cmd)...)

	case StateSSHAuthInput:
		switch msg := msg.(type) {
		case tea.KeyPressMsg:
			switch msg.String() {
			case "enter":
				password := m.sshAuthInput.Value()
				alias := m.sshAuthAlias
				srv := m.sshAuthServer
				svc := m.serverService
				m.state = StateList
				var statusCmd tea.Cmd
				m.statusBar, statusCmd = m.statusBar.Show("Connecting to "+alias+"...", "")
				return m, tea.Batch(statusCmd, func() tea.Msg {
					if err := svc.EstablishSSHMasterWithPassword(alias, password); err != nil {
						return SSHMasterFailedMsg{Alias: alias, Err: err}
					}
					return SSHMasterReadyMsg{Server: srv}
				})
			case "esc":
				m.state = StateList
				return m, nil
			}
		}
		var inputCmd tea.Cmd
		m.sshAuthInput, inputCmd = m.sshAuthInput.Update(msg)
		return m, tea.Batch(append(cmds, inputCmd)...)

	case StateSessionView:
		return m.updateStateSession(msg, cmds)
	}

	return m, tea.Batch(cmds...)
}

func (m AppModel) updateStateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()

		// Terminal overlay is focused → forward keys to PTY except escape combos.
		if m.terminalFocused && m.activeSessionAlias != "" && !m.searching {
			switch key {
			case "esc":
				// Blur terminal → refocus left panel (session stays alive).
				m.terminalFocused = false
				return m, tea.Batch(cmds...)
			case "ctrl+n":
				m.sessions.nextTab()
				if tab := m.sessions.activeTab(); tab != nil {
					m.activeSessionAlias = tab.server.Alias
				}
				return m, tea.Batch(cmds...)
			case "ctrl+p":
				m.sessions.prevTab()
				if tab := m.sessions.activeTab(); tab != nil {
					m.activeSessionAlias = tab.server.Alias
				}
				return m, tea.Batch(cmds...)
			default:
				if tab := m.sessions.findByAlias(m.activeSessionAlias); tab != nil && !tab.closed {
					if data := keyToBytes(key); data != nil {
						tab.writeInput(data)
					}
				}
				return m, tea.Batch(cmds...)
			}
		}

		// Left panel is focused (overlay may be visible but not focused).
		// Ctrl+D closes the session for the currently selected server.
		if key == "ctrl+d" && !m.searching {
			if s := m.selectedServer(); s != nil {
				m.sessions.removeByAlias(s.Alias)
				if m.activeSessionAlias == s.Alias {
					m.activeSessionAlias = ""
					m.terminalFocused = false
				}
				m.updateListItems()
			}
			return m, tea.Batch(cmds...)
		}
		// If searching, handle search input first
		if m.searching {
			switch msg.String() {
			case "esc":
				m.searching = false
				m.search.Blur()
				m.search.SetValue("")
				cmds = append(cmds, loadServers(m.serverService, ""))
				return m, tea.Batch(cmds...)
			case "enter":
				m.searching = false
				m.search.Blur()
				return m, tea.Batch(cmds...)
			default:
				var cmd tea.Cmd
				m.search, cmd = m.search.Update(msg)
				cmds = append(cmds, cmd)
				q := m.search.Value()
				if strings.Contains(q, "#") {
					tags, text := parseSearchTokens(q)
					filtered := filterServers(m.servers, tags, text)
					items := make([]list.Item, len(filtered))
					for i, s := range filtered {
						items[i] = ServerItem{Server: s, PingStatus: m.pingStatus[s.Alias]}
					}
					m.list.SetItems(items)
					m.updateDetailsForSelected()
				} else {
					cmds = append(cmds, loadServers(m.serverService, q))
				}
				return m, tea.Batch(cmds...)
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "/":
			m.searching = true
			m.search.Focus()
			return m, textinput.Blink

		case "a":
			m.form = NewFormModel(FormAdd, nil)
			m.state = StateAddForm
			return m, m.form.Init()

		case "e":
			if s := m.selectedServer(); s != nil {
				clone := *s
				m.form = NewFormModel(FormEdit, &clone)
				m.state = StateEditForm
				return m, m.form.Init()
			}

		case "d":
			if s := m.selectedServer(); s != nil {
				clone := *s
				m.deleteTarget = &clone
				m.state = StateDeleteConfirm
				return m, nil
			}

		case "p":
			if s := m.selectedServer(); s != nil {
				pinned := !s.PinnedAt.IsZero()
				if err := m.serverService.SetPinned(s.Alias, !pinned); err != nil {
					var cmd tea.Cmd
					m.statusBar, cmd = m.statusBar.Show("Pin failed: "+err.Error(), "#f7768e")
					cmds = append(cmds, cmd)
				} else {
					action := "Pinned"
					if pinned {
						action = "Unpinned"
					}
					var cmd tea.Cmd
					m.statusBar, cmd = m.statusBar.Show(action+" "+s.Alias, "")
					cmds = append(cmds, cmd)
					cmds = append(cmds, loadServers(m.serverService, m.search.Value()))
				}
			}

		case "g":
			if s := m.selectedServer(); s != nil {
				clone := *s
				var cmd tea.Cmd
				m.statusBar, cmd = m.statusBar.Show("Pinging "+s.Alias+"...", "")
				m.pingNotice = "Pinging " + s.Alias + "..."
				cmds = append(cmds, cmd)
				cmds = append(cmds, pingServer(m.serverService, clone))
			}

		case "r":
			cmds = append(cmds, loadServers(m.serverService, m.search.Value()))
			cmds = append(cmds, pingAllServers(m.serverService, m.servers))

		case "c":
			if s := m.selectedServer(); s != nil {
				sshCmd := BuildSSHCommand(*s)
				err := clipboard.WriteAll(sshCmd)
				var cmd tea.Cmd
				if err != nil {
					m.statusBar, cmd = m.statusBar.Show("Copy failed: "+err.Error(), "#f7768e")
				} else {
					m.statusBar, cmd = m.statusBar.Show("Copied: "+sshCmd, "")
				}
				cmds = append(cmds, cmd)
			}

		case "f":
			if s := m.selectedServer(); s != nil {
				if m.serverService.SSHMasterExists(s.Alias) {
					// ControlMaster already up — enter file browser immediately.
					m.fileBrowser = NewFileBrowserModel(*s, m.serverService, m.width, m.height)
					m.state = StateFileBrowser
					return m, m.fileBrowser.Init()
				}
				// First try a no-password batch connection (key-based auth).
				clone := *s
				svc := m.serverService
				return m, func() tea.Msg {
					if svc.SSHMasterExists(clone.Alias) {
						return SSHMasterReadyMsg{Server: clone}
					}
					if err := svc.TrySSHMasterBatch(clone.Alias); err == nil {
						return SSHMasterReadyMsg{Server: clone}
					}
					// Batch failed — need password/OTP from the user.
					return SSHAuthInputMsg{}
				}
			}

		case "F":
			if s := m.selectedServer(); s != nil {
				_, err := m.serverService.StartForward(s.Alias, nil)
				var cmd tea.Cmd
				if err != nil {
					m.statusBar, cmd = m.statusBar.Show("Forward failed: "+err.Error(), "#f7768e")
				} else {
					m.statusBar, cmd = m.statusBar.Show("Forwarding started for "+s.Alias, "")
				}
				cmds = append(cmds, cmd)
			}

		case "x":
			if s := m.selectedServer(); s != nil {
				err := m.serverService.StopForwarding(s.Alias)
				var cmd tea.Cmd
				if err != nil {
					m.statusBar, cmd = m.statusBar.Show("Stop forward failed: "+err.Error(), "#f7768e")
				} else {
					m.statusBar, cmd = m.statusBar.Show("Forwarding stopped for "+s.Alias, "")
				}
				cmds = append(cmds, cmd)
			}

		case "s":
			m.sortMode = (m.sortMode + 1) % 4
			var cmd tea.Cmd
			m.statusBar, cmd = m.statusBar.Show(sortModeName(m.sortMode), "")
			cmds = append(cmds, cmd)

		case "right":
			// Open or focus an embedded PTY session for the selected server.
			if s := m.selectedServer(); s != nil {
				// If session already exists, focus its overlay.
				if existing := m.sessions.findByAlias(s.Alias); existing != nil {
					m.activeSessionAlias = s.Alias
					m.terminalFocused = true
					return m, tea.Batch(cmds...)
				}
				tab, err := newSession(*s, m.sessions.termCols(), m.sessions.termRows())
				if err != nil {
					var statusCmd tea.Cmd
					m.statusBar, statusCmd = m.statusBar.Show("Session failed: "+err.Error(), "#f7768e")
					cmds = append(cmds, statusCmd)
				} else {
					m.sessions.addSession(tab)
					m.activeSessionAlias = s.Alias
					m.terminalFocused = true
					m.updateListItems()
					return m, tea.Batch(append(cmds, waitForPtyOutput(tab))...)
				}
			}

		case "enter":
			if s := m.selectedServer(); s != nil {
				alias := s.Alias
				sshCmd := exec.Command("ssh", alias)
				return m, tea.ExecProcess(sshCmd, func(err error) tea.Msg {
					if err != nil {
						return StatusMsg{Text: "SSH error: " + err.Error(), Color: "#f7768e"}
					}
					return loadServers(m.serverService, "")()
				})
			}

		case "j", "down":
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			cmds = append(cmds, cmd)
			m.updateDetailsForSelected()
			return m, tea.Batch(cmds...)

		case "k", "up":
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			cmds = append(cmds, cmd)
			m.updateDetailsForSelected()
			return m, tea.Batch(cmds...)

		case "?":
			m.help = NewHelpModel(m.width, m.height)
			m.state = StateHelp
			return m, nil

		case " ":
			if s := m.selectedServer(); s != nil {
				m.previewText = formatQuickPreview(*s)
				m.state = StateQuickPreview
				return m, nil
			}
		}
	}

	// Default: pass message to list
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	cmds = append(cmds, cmd)
	m.updateDetailsForSelected()
	return m, tea.Batch(cmds...)
}

func (m AppModel) updateStateSession(msg tea.Msg, cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, tea.Batch(cmds...)
	}

	key := kp.String()

	// Ctrl+A is our prefix key (like tmux's Ctrl+B).
	// We track it via a simple flag embedded as a prefix in the key string.
	// bubbletea reports it as "ctrl+a".
	switch key {
	case "ctrl+a":
		// Next key will be interpreted as a session command; handled via a
		// lightweight convention: store a flag and wait for the next event.
		// For simplicity, use dedicated shortcuts directly.
		return m, tea.Batch(cmds...)

	// Session-management shortcuts (no prefix required to keep it simple).
	case "ctrl+\\": // return to list view, sessions stay alive
		m.state = StateList
		return m, tea.Batch(cmds...)

	case "ctrl+n": // next tab
		m.sessions.nextTab()
		return m, tea.Batch(cmds...)

	case "ctrl+p": // prev tab
		m.sessions.prevTab()
		return m, tea.Batch(cmds...)

	case "ctrl+w": // close active tab
		if tab := m.sessions.activeTab(); tab != nil {
			alias := tab.server.Alias
			m.sessions.removeByAlias(alias)
			if len(m.sessions.tabs) == 0 {
				m.state = StateList
			}
		}
		return m, tea.Batch(cmds...)
	}

	// Forward all other keys to the active PTY.
	if tab := m.sessions.activeTab(); tab != nil && !tab.closed {
		if data := keyToBytes(key); data != nil {
			tab.writeInput(data)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m AppModel) updateStateDelete(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			if m.deleteTarget != nil {
				if err := m.serverService.DeleteServer(*m.deleteTarget); err != nil {
					var cmd tea.Cmd
					m.statusBar, cmd = m.statusBar.Show("Delete failed: "+err.Error(), "#f7768e")
					m.state = StateList
					m.deleteTarget = nil
					return m, cmd
				}
				var statusCmd tea.Cmd
				m.statusBar, statusCmd = m.statusBar.Show("Deleted "+m.deleteTarget.Alias, "")
				m.deleteTarget = nil
				m.state = StateList
				return m, tea.Batch(statusCmd, loadServers(m.serverService, m.search.Value()))
			}
			m.state = StateList
		case "n", "N", "esc", "q":
			m.deleteTarget = nil
			m.state = StateList
		}
	}
	return m, nil
}

func (m *AppModel) handleFormSave(msg FormSaveMsg) tea.Cmd {
	var err error
	if msg.Original != nil {
		err = m.serverService.UpdateServer(*msg.Original, msg.Server)
	} else {
		err = m.serverService.AddServer(msg.Server)
	}
	if err != nil {
		var cmd tea.Cmd
		m.statusBar, cmd = m.statusBar.Show("Save failed: "+err.Error(), "#f7768e")
		return cmd
	}
	var cmd tea.Cmd
	m.statusBar, cmd = m.statusBar.Show("Saved "+msg.Server.Alias, "")
	return tea.Batch(cmd, loadServers(m.serverService, m.search.Value()))
}

// View renders the entire TUI.
func (m AppModel) View() tea.View {
	var content string
	if m.width == 0 {
		content = "Loading..."
	} else {
		// Overlay states
		switch m.state {
		case StateAddForm, StateEditForm:
			// Render main view as background, then overlay the form.
			bg := m.viewBackground()
			formStr := m.form.View().Content
			content = overlayCenter(bg, formStr, m.width, m.height)

		case StateDeleteConfirm:
			content = m.viewDeleteConfirm()

		case StateFormPreview:
			content = m.viewFormPreview()

		case StateHelp:
			content = m.viewHelp()

		case StateQuickPreview:
			content = m.viewQuickPreview()

		case StateFileBrowser:
			v := tea.NewView(m.fileBrowser.ViewString())
			v.AltScreen = true
			return v

		case StateSessionView:
			v := tea.NewView(m.renderSessionView())
			v.AltScreen = true
			return v

		case StateSSHAuthInput:
			bg := m.viewBackground()
			overlay := m.viewSSHAuthInput()
			content = overlayCenter(bg, overlay, m.width, m.height)

		default:
			// Normal list view
			c := m.viewContent()
			var statusBar string
			if m.activeSessionAlias != "" {
				focusHint := "→:enter   Esc:back to list"
				if m.terminalFocused {
					focusHint = "Esc:back to list   ^D:close"
				}
				sessionInfo := lipgloss.NewStyle().
					Foreground(colorDim).
					Render("  " + m.activeSessionAlias + "   " + focusHint)
				if bar := m.statusBar.View(m.width); bar != "" {
					statusBar = bar
				} else {
					statusBar = sessionInfo
				}
			} else {
				statusBar = m.statusBar.View(m.width)
			}
			content = lipgloss.JoinVertical(lipgloss.Left, c, statusBar)
		}
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// viewSSHAuthInput renders the centered SSH authentication overlay.
func (m AppModel) viewSSHAuthInput() string {
	title := KeyStyle.Render("SSH Authentication")
	subtitle := HelpStyle.Render("Server: " + m.sshAuthAlias)
	hint := HelpStyle.Render("Enter: confirm   Esc: cancel")

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		subtitle,
		"",
		m.sshAuthInput.View(),
		"",
		hint,
	)
	return FormBorderStyle.Width(60).Render(content)
}

// viewBackground renders the normal main view as a background string (no header).
func (m AppModel) viewBackground() string {
	c := m.viewContent()
	statusBar := m.statusBar.View(m.width)
	return lipgloss.JoinVertical(lipgloss.Left, c, statusBar)
}

// overlayCenter centers box over base within a w×h area, splicing the box
// into the base line by line. Adapted from sqltui's Composite function.
// Uses github.com/charmbracelet/x/ansi for ANSI-aware width/truncation.
func overlayCenter(base, box string, w, h int) string {
	if w <= 0 || h <= 0 {
		return base
	}

	baseLines := strings.Split(base, "\n")
	for len(baseLines) < h {
		baseLines = append(baseLines, "")
	}
	if len(baseLines) > h {
		baseLines = baseLines[:h]
	}

	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		if lw := ansi.StringWidth(l); lw > bw {
			bw = lw
		}
	}
	if bw > w {
		bw = w
	}
	bh := len(boxLines)
	if bh > h {
		boxLines = boxLines[:h]
		bh = h
	}

	// Center position
	x := (w - bw) / 2
	y := (h - bh) / 2

	out := make([]string, len(baseLines))
	for i, bl := range baseLines {
		// Pad background line to full width
		if pad := w - ansi.StringWidth(bl); pad > 0 {
			bl += strings.Repeat(" ", pad)
		}
		j := i - y
		if j < 0 || j >= bh {
			out[i] = bl
			continue
		}
		// Splice box line at column x
		line := ansi.Truncate(boxLines[j], bw, "")
		if pad := bw - ansi.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		left := ansi.Truncate(bl, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(bl, x+bw, "")
		// Fix double-width rune straddle at cut boundary
		if d := ansi.StringWidth(right) - (w - x - bw); d > 0 {
			right = strings.Repeat(" ", d) + ansi.TruncateLeft(bl, x+bw+d, "")
		}
		out[i] = left + line + right
	}
	return strings.Join(out, "\n")
}

func (m AppModel) viewContent() string {
	h := m.height
	w := m.width
	if h <= 0 || w <= 0 {
		return ""
	}

	// Reserve 1 line for status bar
	bodyH := h - 1
	if bodyH < 3 {
		bodyH = 3
	}

	// Left panel = 1/4, right panel = 3/4 of terminal width.
	leftW := w / 4
	if leftW < 18 {
		leftW = 18
	}
	rightW := w - leftW

	// Left panel: build manually so search never creates a separate box.
	// Border overhead = 2 (top+bottom). Inner rows = bodyH - 2.
	innerRows := bodyH - 2
	if innerRows < 1 {
		innerRows = 1
	}
	innerW := leftW - 4 // 2 border + 2 padding

	// When searching: first inner row is the search input.
	// List gets innerRows-1 rows. When not searching: list gets innerRows rows.
	listRows := innerRows
	var searchLine string
	if m.searching {
		listRows = innerRows - 1
		if listRows < 1 {
			listRows = 1
		}
		// Render search input as a single line inside the left border
		searchLine = lipgloss.NewStyle().
			Foreground(colorAccent).Bold(true).Width(innerW).
			Render("/ " + m.search.View())
	}

	// Resize list to fit
	m.list.SetSize(innerW, listRows)

	// Build left panel: border box containing [optional search] + list
	var leftContent string
	if m.searching {
		leftContent = lipgloss.JoinVertical(lipgloss.Left, searchLine, m.list.View())
	} else {
		leftContent = m.list.View()
	}
	// Width(leftW): lipgloss v2 Width sets outer total, so pass leftW not innerW.
	leftPane := ListBorderStyle.Width(leftW).Height(bodyH).Render(leftContent)

	// Right panel: terminal overlay if a session is active, else config details
	var rightPane string
	if m.activeSessionAlias != "" {
		if tab := m.sessions.findByAlias(m.activeSessionAlias); tab != nil {
			rightPane = renderTerminalOverlay(tab, rightW, bodyH, m.terminalFocused)
		}
	}
	if rightPane == "" {
		m.details.SetSize(rightW, bodyH)
		rightPane = m.details.View().Content
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
}

func (m AppModel) viewDeleteConfirm() string {
	alias := "(unknown)"
	if m.deleteTarget != nil {
		alias = m.deleteTarget.Alias
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		formTitleStyle.Render("Confirm Delete"),
		"",
		fmt.Sprintf("Delete server %s?", KeyStyle.Render(alias)),
		"",
		HelpStyle.Render("y/Enter: confirm   n/Esc: cancel"),
	)

	box := FormBorderStyle.Render(content)

	// Center the box
	lines := strings.Split(box, "\n")
	boxH := len(lines)
	boxW := 0
	for _, l := range lines {
		if lipgloss.Width(l) > boxW {
			boxW = lipgloss.Width(l)
		}
	}
	topPad := (m.height - boxH) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (m.width - boxW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	padStyle := lipgloss.NewStyle().PaddingLeft(leftPad).PaddingTop(topPad)
	return padStyle.Render(box)
}

func sortModeName(mode int) string {
	switch mode {
	case 0:
		return "Sort: Alias A-Z"
	case 1:
		return "Sort: Alias Z-A"
	case 2:
		return "Sort: Last Seen (newest)"
	case 3:
		return "Sort: Last Seen (oldest)"
	}
	return ""
}

// viewHelp renders the help overlay centered over the main view background.
func (m AppModel) viewHelp() string {
	bg := m.viewBackground()
	box := m.help.ViewString()
	return overlayCenter(bg, box, m.width, m.height)
}

// viewQuickPreview renders the centered quick preview overlay.
func (m AppModel) viewQuickPreview() string {
	box := FormBorderStyle.Render(m.previewText)
	lines := strings.Split(box, "\n")
	boxH := len(lines)
	boxW := 0
	for _, l := range lines {
		if lipgloss.Width(l) > boxW {
			boxW = lipgloss.Width(l)
		}
	}
	topPad := (m.height - boxH) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (m.width - boxW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	return lipgloss.NewStyle().PaddingLeft(leftPad).PaddingTop(topPad).Render(box)
}

// updateStateFormPreview handles key presses while the form preview overlay is shown.
func (m AppModel) updateStateFormPreview(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter":
			cmd := m.handleFormSave(FormSaveMsg{
				Server:   m.formPreview.Server,
				Original: m.formPreview.Original,
			})
			m.state = StateList
			return m, cmd
		case "esc":
			if m.formPreview.Original != nil {
				m.state = StateEditForm
			} else {
				m.state = StateAddForm
			}
		}
	}
	return m, nil
}

// viewFormPreview renders the centered SSH config preview overlay.
func (m AppModel) viewFormPreview() string {
	previewStyled := RawConfigKeywordStyle.Render("Host " + m.formPreview.Server.Alias)
	lines := strings.Split(m.formPreview.Preview, "\n")
	var highlighted []string
	highlighted = append(highlighted, previewStyled)
	for _, line := range lines[1:] { // skip the first "Host" line, already rendered
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		idx := strings.IndexAny(trimmed, " \t")
		if idx > 0 {
			highlighted = append(highlighted, indent+
				RawConfigFieldStyle.Render(trimmed[:idx])+" "+
				RawConfigValueStyle.Render(strings.TrimSpace(trimmed[idx:])))
		} else {
			highlighted = append(highlighted, RawConfigFieldStyle.Render(line))
		}
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		formTitleStyle.Render("Preview SSH Config"),
		"",
		strings.Join(highlighted, "\n"),
		"",
		HelpStyle.Render("Enter: confirm   Esc: back to form"),
	)

	box := FormBorderStyle.Render(body)
	boxLines := strings.Split(box, "\n")
	boxH := len(boxLines)
	boxW := 0
	for _, l := range boxLines {
		if lipgloss.Width(l) > boxW {
			boxW = lipgloss.Width(l)
		}
	}
	topPad := (m.height - boxH) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (m.width - boxW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	return lipgloss.NewStyle().PaddingLeft(leftPad).PaddingTop(topPad).Render(box)
}

// formatQuickPreview returns a nicely formatted multi-line string of non-empty server fields.
func formatQuickPreview(s domain.Server) string {
	var sb strings.Builder

	line := func(label, value string) {
		if value == "" {
			return
		}
		sb.WriteString(
			lipgloss.NewStyle().Foreground(colorDim).Width(18).Render(label) +
				lipgloss.NewStyle().Foreground(colorText).Render(value) + "\n",
		)
	}

	sb.WriteString(formTitleStyle.Render(s.Alias) + "\n\n")
	line("Host:", s.Host)
	if s.User != "" {
		line("User:", s.User)
	}
	if s.Port > 0 && s.Port != 22 {
		line("Port:", fmt.Sprintf("%d", s.Port))
	}
	if len(s.Tags) > 0 {
		line("Tags:", strings.Join(s.Tags, ", "))
	}
	if len(s.IdentityFiles) > 0 {
		line("Identity Files:", strings.Join(s.IdentityFiles, ", "))
	}
	if s.ProxyJump != "" {
		line("ProxyJump:", s.ProxyJump)
	}
	if s.ProxyCommand != "" {
		line("ProxyCommand:", s.ProxyCommand)
	}
	if len(s.LocalForward) > 0 {
		line("LocalForward:", strings.Join(s.LocalForward, ", "))
	}
	if len(s.RemoteForward) > 0 {
		line("RemoteForward:", strings.Join(s.RemoteForward, ", "))
	}
	if len(s.DynamicForward) > 0 {
		line("DynamicForward:", strings.Join(s.DynamicForward, ", "))
	}
	if !s.LastSeen.IsZero() {
		line("Last Seen:", s.LastSeen.Format("2006-01-02 15:04"))
	}
	if s.SSHCount > 0 {
		line("SSH Count:", fmt.Sprintf("%d", s.SSHCount))
	}
	sb.WriteString("\n" + HelpStyle.Render("Press any key to close"))
	return sb.String()
}

// parseSearchTokens splits a query string into tag filters and a plain text query.
// Tokens beginning with "#" are tag filters; all others are joined as the text query.
func parseSearchTokens(q string) (tags []string, text string) {
	tokens := strings.Fields(q)
	var textTokens []string
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "#") {
			tag := strings.TrimPrefix(tok, "#")
			if tag != "" {
				tags = append(tags, strings.ToLower(tag))
			}
		} else {
			textTokens = append(textTokens, tok)
		}
	}
	text = strings.Join(textTokens, " ")
	return
}

// filterServers returns the subset of servers that match all requested tags and
// contain the text query (case-insensitive substring) in alias or host.
func filterServers(servers []domain.Server, tags []string, text string) []domain.Server {
	var result []domain.Server
	lowerText := strings.ToLower(text)
	for _, s := range servers {
		// Tag filter: server must satisfy every requested tag.
		if len(tags) > 0 {
			allMatched := true
			for _, tag := range tags {
				found := false
				for _, st := range s.Tags {
					if strings.EqualFold(st, tag) {
						found = true
						break
					}
				}
				if !found {
					allMatched = false
					break
				}
			}
			if !allMatched {
				continue
			}
		}
		// Text filter: substring match against alias + host.
		if lowerText != "" {
			combined := strings.ToLower(s.Alias + " " + s.Host)
			if !strings.Contains(combined, lowerText) {
				continue
			}
		}
		result = append(result, s)
	}
	return result
}
