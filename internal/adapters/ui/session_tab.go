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
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	tea "charm.land/bubbletea/v2"
	"github.com/linpr/lazyssh/internal/core/domain"
)

// PtyOutputMsg is sent to the program when a PTY session has new output.
type PtyOutputMsg struct{ Alias string }

// SessionClosedMsg is sent when an SSH session process exits.
type SessionClosedMsg struct{ Alias string }

// sessionListWidth is the fixed column width of the left sessions panel.
const sessionListWidth = 22

// SessionTab holds one active SSH session: its PTY master, vt10x terminal
// emulator state, and the channel used to notify the TUI of new output.
type SessionTab struct {
	server domain.Server
	cmd    *exec.Cmd
	ptm    *os.File
	term   vt10x.Terminal
	outCh  chan struct{}
	closed bool
}

// SessionsModel is the connection pool of active SSH sessions.
type SessionsModel struct {
	tabs   []*SessionTab
	active int
	width  int
	height int
}

// newSession starts "ssh <alias>" inside a PTY, wires it to a vt10x terminal
// emulator, and launches the goroutine that feeds PTY bytes into the emulator.
func newSession(server domain.Server, cols, rows int) (*SessionTab, error) {
	// #nosec G204
	cmd := exec.Command("ssh", server.Alias)

	ptm, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}
	if err := pty.Setsize(ptm, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}); err != nil {
		_ = ptm.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("pty setsize: %w", err)
	}

	// WithWriter routes OSC/DA responses from the terminal emulator back to ssh.
	term := vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(ptm))

	tab := &SessionTab{
		server: server,
		cmd:    cmd,
		ptm:    ptm,
		term:   term,
		outCh:  make(chan struct{}, 1),
	}
	go tab.readLoop()
	return tab, nil
}

// readLoop drains the PTY master, feeds bytes into the vt10x emulator, and
// signals outCh on every non-empty read. Closes outCh on EOF.
func (t *SessionTab) readLoop() {
	defer func() {
		t.closed = true
		close(t.outCh)
	}()

	buf := make([]byte, 4096)
	for {
		n, err := t.ptm.Read(buf)
		if n > 0 {
			// Write() acquires its own internal lock — do NOT call t.term.Lock() here.
			_, _ = t.term.Write(buf[:n])
			select {
			case t.outCh <- struct{}{}:
			default:
			}
		}
		if err != nil {
			return
		}
	}
}

// writeInput forwards keyboard bytes to the PTY (and thus to the ssh process).
func (t *SessionTab) writeInput(data []byte) {
	_, _ = t.ptm.Write(data)
}

// resize updates both the OS PTY and the terminal emulator dimensions.
func (t *SessionTab) resize(cols, rows int) {
	_ = pty.Setsize(t.ptm, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	t.term.Resize(cols, rows)
}

// close terminates the SSH process and the PTY file descriptor.
func (t *SessionTab) close() {
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.ptm.Close()
}

// waitForPtyOutput returns a Cmd that blocks until the session produces new
// output or exits.
func waitForPtyOutput(tab *SessionTab) tea.Cmd {
	return func() tea.Msg {
		_, ok := <-tab.outCh
		if !ok {
			return SessionClosedMsg{Alias: tab.server.Alias}
		}
		return PtyOutputMsg{Alias: tab.server.Alias}
	}
}

// ---- SessionsModel helpers ----

func (m *SessionsModel) addSession(tab *SessionTab) {
	tab.resize(m.termCols(), m.termRows())
	m.tabs = append(m.tabs, tab)
	m.active = len(m.tabs) - 1
}

func (m *SessionsModel) removeByAlias(alias string) {
	for i, t := range m.tabs {
		if t.server.Alias == alias {
			t.close()
			m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
			if m.active >= len(m.tabs) && m.active > 0 {
				m.active--
			}
			return
		}
	}
}

func (m *SessionsModel) findByAlias(alias string) *SessionTab {
	for _, t := range m.tabs {
		if t.server.Alias == alias {
			return t
		}
	}
	return nil
}

func (m *SessionsModel) activeTab() *SessionTab {
	if len(m.tabs) == 0 || m.active >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.active]
}

func (m *SessionsModel) nextTab() {
	if len(m.tabs) > 0 {
		m.active = (m.active + 1) % len(m.tabs)
	}
}

func (m *SessionsModel) prevTab() {
	if len(m.tabs) > 0 {
		m.active = (m.active - 1 + len(m.tabs)) % len(m.tabs)
	}
}

// termCols / termRows: PTY dimensions matching the overlay content area.
// Uses same leftW ratio as viewContent (w/4) and overlay inner = rightW - 2.
func (m *SessionsModel) termCols() int {
	leftW := m.width / 4
	if leftW < 18 {
		leftW = 18
	}
	c := m.width - leftW - 2 // rightW - 2 = overlay inner
	if c < 20 {
		return 20
	}
	return c
}

func (m *SessionsModel) termRows() int {
	r := m.height - 3 // bodyH(h-1) - 2 overlay border rows = h-3
	if r < 5 {
		return 5
	}
	return r
}

func (m *SessionsModel) resizeAll() {
	cols, rows := m.termCols(), m.termRows()
	for _, t := range m.tabs {
		t.resize(cols, rows)
	}
}
