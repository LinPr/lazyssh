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
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// echoTab returns a session backed by a tiny shell that echoes every line back
// with a GOT: prefix, avoiding any network dependency in tests.
func echoTab(t *testing.T, cols, rows int) *SessionTab {
	t.Helper()
	cmd := exec.Command("sh", "-c", `while read -r line; do echo "GOT:$line"; done`)
	ptm, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	tab := &SessionTab{
		cmd:   cmd,
		ptm:   ptm,
		term:  vt.NewEmulator(cols, rows),
		outCh: make(chan struct{}, 1),
	}
	go tab.readLoop()
	go tab.responseLoop()
	t.Cleanup(tab.close)
	time.Sleep(150 * time.Millisecond)
	return tab
}

// historyTab returns an offline session pre-filled with 60 lines of scrollback.
func historyTab(t *testing.T, cols, rows int) *SessionTab {
	t.Helper()
	tab := &SessionTab{
		term:  vt.NewEmulator(cols, rows),
		outCh: make(chan struct{}, 1),
	}
	tab.term.SetScrollbackSize(200)
	for i := 0; i < 60; i++ {
		fmt.Fprintf(tab.term, "history %02d\r\n", i)
	}
	return tab
}

// TestScrollbackKeepsHistory is the core regression test for "the pane fills up
// and cannot be scrolled": vt10x kept no history at all.
func TestScrollbackKeepsHistory(t *testing.T) {
	tab := historyTab(t, 40, 6)
	if got := tab.term.ScrollbackLen(); got == 0 {
		t.Fatal("no scrollback recorded")
	}

	if tab.isScrolledBack() {
		t.Fatal("a fresh session must be at the bottom")
	}

	live := tab.renderGrid(40, 6)
	if !tab.scrollBy(10) {
		t.Fatal("scrollBy(10) reported no change")
	}
	scrolled := tab.renderGrid(40, 6)
	if live == scrolled {
		t.Fatal("scrolled view is identical to the live view")
	}

	// The visible window must move up by exactly the requested amount.
	liveTop, scrolledTop := firstLine(live), firstLine(scrolled)
	if idxOf(t, liveTop) <= idxOf(t, scrolledTop) {
		t.Fatalf("window did not move up: live=%q scrolled=%q", liveTop, scrolledTop)
	}

	tab.scrollToBottom()
	if tab.isScrolledBack() {
		t.Fatal("scrollToBottom did not reset the offset")
	}
}

// TestScrollClampsToHistory makes sure the offset cannot run past the buffer.
func TestScrollClampsToHistory(t *testing.T) {
	tab := historyTab(t, 40, 6)
	limit := tab.term.ScrollbackLen()

	tab.scrollBy(1_000_000)
	if got := tab.scrolledLines(); got != limit {
		t.Fatalf("offset = %d, want clamped to %d", got, limit)
	}
	tab.scrollBy(-1_000_000)
	if got := tab.scrolledLines(); got != 0 {
		t.Fatalf("offset = %d, want 0", got)
	}
}

// TestWheelScrollsSessionView drives the real Update path.
func TestWheelScrollsSessionView(t *testing.T) {
	tab := historyTab(t, 50, 8)
	app := NewLayoutTestApp(120, 30)
	app.sessions.addSession(tab)
	app.state = StateSessionView
	app.activeSessionAlias = "demo"

	var model tea.Model = app
	for i := 0; i < 3; i++ {
		model, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	}
	up := model.(AppModel)
	if !up.sessions.tabs[0].isScrolledBack() {
		t.Fatal("wheel up did not scroll back")
	}

	for i := 0; i < 20; i++ {
		model, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}
	if model.(AppModel).sessions.tabs[0].isScrolledBack() {
		t.Fatal("wheel down did not return to the live view")
	}
}

// TestTypingReturnsToLive makes sure input does not get "lost" off screen.
func TestTypingReturnsToLive(t *testing.T) {
	// Needs history so the scrolled state is actually reachable.
	tab := historyTab(t, 50, 8)
	app := NewLayoutTestApp(120, 30)
	app.sessions.addSession(tab)
	app.state = StateSessionView
	app.activeSessionAlias = "demo"

	tab.scrollBy(5)
	if !tab.isScrolledBack() {
		t.Fatal("setup: expected scrolled state")
	}

	var model tea.Model = app
	model, _ = model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if model.(AppModel).sessions.tabs[0].isScrolledBack() {
		t.Fatal("typing did not snap back to the live view")
	}
}

// TestPasteReachesChild covers the paste path end to end through Update.
func TestPasteReachesChild(t *testing.T) {
	tab := echoTab(t, 60, 10)
	app := NewLayoutTestApp(120, 30)
	app.sessions.addSession(tab)
	app.state = StateSessionView
	app.activeSessionAlias = "demo"

	var model tea.Model = app
	model, _ = model.Update(tea.PasteMsg{Content: "PASTED" + "\n"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tab.mu.Lock()
		screen := tab.term.String()
		tab.mu.Unlock()
		if strings.Contains(screen, "GOT:PASTED") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	tab.mu.Lock()
	screen := tab.term.String()
	tab.mu.Unlock()
	t.Fatalf("pasted text never reached the child; screen=%q", screen)
}

// TestScrollIndicatorAppears checks the status bar tells the user where they are.
func TestScrollIndicatorAppears(t *testing.T) {
	tab := historyTab(t, 50, 8)
	app := NewLayoutTestApp(120, 30)
	app.sessions.addSession(tab)
	app.state = StateSessionView
	app.activeSessionAlias = "demo"

	if strings.Contains(app.sessionStatusBar(), "history") {
		t.Fatal("fresh session should not show the history hint")
	}

	tab.scrollBy(6)
	bar := app.sessionStatusBar()
	if !strings.Contains(bar, "history") {
		t.Fatalf("status bar does not mention history: %q", bar)
	}
}

// TestSessionViewFitsTerminal is the regression test for the double-newline bug
// that pushed the status bar off screen.
func TestSessionViewFitsTerminal(t *testing.T) {
	tab := historyTab(t, 50, 8)
	app := NewLayoutTestApp(120, 30)
	app.sessions.addSession(tab)
	app.state = StateSessionView

	lines := strings.Split(app.renderSessionView(), "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d lines, want 30", len(lines))
	}
	// The last line must be the status bar, not blank filler.
	if strings.TrimSpace(lines[len(lines)-1]) == "" {
		t.Fatalf("last line is empty, status bar was pushed off screen: %q", lines[len(lines)-1])
	}
}

// TestAltScreenWheelGoesToRemote proves the wheel is not stolen from vim/less.
func TestAltScreenWheelGoesToRemote(t *testing.T) {
	tab := historyTab(t, 50, 8)
	tab.term.Write([]byte("\x1b[?1049h")) // enter alternate screen
	if !tab.altScreen() {
		t.Fatal("alternate screen not detected")
	}

	app := NewLayoutTestApp(120, 30)
	app.sessions.addSession(tab)
	app.state = StateSessionView

	var model tea.Model = app
	model, _ = model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if model.(AppModel).sessions.tabs[0].isScrolledBack() {
		t.Fatal("wheel scrolled local history while the remote owns the screen")
	}
}

// firstLine returns the first line of a rendered grid.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// idxOf extracts the trailing number of a "history NN" line.
func idxOf(t *testing.T, line string) int {
	t.Helper()
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return -1
	}
	var n int
	if _, err := fmt.Sscanf(fields[len(fields)-1], "%d", &n); err != nil {
		return -1
	}
	return n
}
