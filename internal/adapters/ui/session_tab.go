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
	"os"
	"os/exec"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/linpr/lazyssh/internal/core/domain"
)

// PtyOutputMsg is sent to the program when a PTY session has new output.
type PtyOutputMsg struct{ Alias string }

// SessionClosedMsg is sent when an SSH session process exits.
type SessionClosedMsg struct{ Alias string }

// scrollbackLines is how much history each session keeps for wheel/PgUp
// scrolling. 5000 lines is roughly a screen-and-a-half per 30-row terminal ×160.
const scrollbackLines = 5000

// SessionTab holds one active SSH session: its PTY master, terminal emulator
// state, and the channel used to notify the TUI of new output.
type SessionTab struct {
	server domain.Server
	cmd    *exec.Cmd
	ptm    *os.File
	term   *vt.Emulator
	outCh  chan struct{}
	closed bool

	// mu guards term and the lifecycle fields below: the reader goroutine writes
	// to them while the UI goroutine reads them. *vt.Emulator is not internally
	// synchronised, and `closed` is set on the reader goroutine.
	mu sync.Mutex

	// scrollOffset counts lines scrolled back from the live view: 0 is the
	// bottom, positive values look further into history.
	scrollOffset int
}

// SessionsModel is the connection pool of active SSH sessions.
type SessionsModel struct {
	tabs   []*SessionTab
	active int
	width  int
	height int
}

// newSession starts "ssh <alias>" inside a PTY, wires it to a terminal emulator
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

	term := vt.NewEmulator(cols, rows)
	// Keep history so the wheel can scroll back through it.
	term.SetScrollbackSize(scrollbackLines)

	tab := &SessionTab{
		server: server,
		cmd:    cmd,
		ptm:    ptm,
		term:   term,
		outCh:  make(chan struct{}, 1),
	}
	go tab.readLoop()
	// Terminal replies (device attributes, mode reports, OSC answers) are written
	// to the emulator's input pipe; forward them to ssh or it hangs waiting.
	go tab.responseLoop()
	return tab, nil
}

// responseLoop forwards the emulator's replies back to the child process.
func (t *SessionTab) responseLoop() {
	buf := make([]byte, 1024)
	for {
		n, err := t.term.Read(buf)
		if n > 0 {
			_, _ = t.ptm.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// readLoop drains the PTY master, feeds bytes into the terminal emulator, and
// signals outCh on every non-empty read. Closes outCh on EOF.
func (t *SessionTab) readLoop() {
	defer func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		close(t.outCh)
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := t.ptm.Read(buf)
		if n > 0 {
			t.mu.Lock()
			_, _ = t.term.Write(buf[:n])
			// New output while scrolled back must not move the viewport, but the
			// offset has to stay inside the (possibly larger) history buffer.
			if t.scrollOffset > 0 {
				if avail := t.term.ScrollbackLen(); t.scrollOffset > avail {
					t.scrollOffset = avail
				}
			}
			t.mu.Unlock()

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

// isClosed reports whether the session process has exited.
func (t *SessionTab) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// writeInput forwards keyboard bytes to the PTY (and thus to the ssh process).
func (t *SessionTab) writeInput(data []byte) {
	_, _ = t.ptm.Write(data)
}

// pasteTypes text into the session. Bracketed paste is used when the remote
// shell enabled it, so multi-line pastes are not executed line by line.
func (t *SessionTab) paste(text string) {
	if text == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.term.Paste(text)
}

// scrollBy moves the viewport by delta lines (negative = older).
// It returns true when the offset changed.
func (t *SessionTab) scrollBy(delta int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	maxOffset := t.term.ScrollbackLen()
	next := t.scrollOffset + delta
	if next < 0 {
		next = 0
	}
	if next > maxOffset {
		next = maxOffset
	}
	if next == t.scrollOffset {
		return false
	}
	t.scrollOffset = next
	return true
}

// scrollToBottom returns the viewport to the live output.
func (t *SessionTab) scrollToBottom() {
	t.mu.Lock()
	t.scrollOffset = 0
	t.mu.Unlock()
}

// isScrolledBack reports whether the viewport is showing history.
func (t *SessionTab) isScrolledBack() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollOffset > 0
}

// scrolledLines reports how many lines of history the viewport is showing.
func (t *SessionTab) scrolledLines() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollOffset
}

// altScreen reports whether the remote program took over the alternate screen
// (vim, less, htop…). In that mode the wheel belongs to the remote program.
func (t *SessionTab) altScreen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.term.IsAltScreen()
}

// sendMouse forwards a mouse event to the remote program, which only has an
// effect when it enabled mouse reporting itself.
func (t *SessionTab) sendMouse(m vt.Mouse) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.term.SendMouse(m)
}

// resize updates both the OS PTY and the terminal emulator dimensions.
func (t *SessionTab) resize(cols, rows int) {
	_ = pty.Setsize(t.ptm, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	t.mu.Lock()
	t.term.Resize(cols, rows)
	t.mu.Unlock()
}

// close terminates the SSH process and the PTY file descriptor.
func (t *SessionTab) close() {
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	_ = t.ptm.Close()

	// Unblock responseLoop by closing the emulator's input pipe rather than
	// calling term.Close(). Emulator.Close() writes an unsynchronised `closed`
	// flag that Emulator.Read() reads from another goroutine, which the race
	// detector flags (x/vt v0.1.0). Closing the pipe is documented-safe for
	// concurrent use and makes the blocked Read return io.EOF.
	if c, ok := t.term.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
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
