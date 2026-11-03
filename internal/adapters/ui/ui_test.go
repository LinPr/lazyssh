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
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/linpr/lazyssh/internal/core/ports"
)

func TestSortServers(t *testing.T) {
	now := time.Now()
	mk := func(alias string, count int, seen time.Time, pinned bool) domain.Server {
		s := domain.Server{Alias: alias, Host: alias + ".example.com", SSHCount: count, LastSeen: seen}
		if pinned {
			s.PinnedAt = now
		}
		return s
	}
	base := func() []domain.Server {
		return []domain.Server{
			mk("charlie", 1, now.Add(-2*time.Hour), false),
			mk("alpha", 9, now.Add(-1*time.Hour), false),
			mk("bravo", 3, now, false),
			mk("pinned", 0, time.Time{}, true),
		}
	}

	tests := []struct {
		mode SortMode
		want []string
	}{
		// Pinned always leads; the rest follow the requested ordering.
		{SortAliasAsc, []string{"pinned", "alpha", "bravo", "charlie"}},
		{SortAliasDesc, []string{"pinned", "charlie", "bravo", "alpha"}},
		{SortLastSeenDesc, []string{"pinned", "bravo", "alpha", "charlie"}},
		{SortMostUsed, []string{"pinned", "alpha", "bravo", "charlie"}},
	}

	for _, tt := range tests {
		servers := base()
		sortServers(servers, tt.mode)
		var got []string
		for _, s := range servers {
			got = append(got, s.Alias)
		}
		if !equalAliases(got, tt.want) {
			t.Errorf("%v: got %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestSortModeCycle(t *testing.T) {
	mode := SortAliasAsc
	seen := map[SortMode]bool{}
	for i := 0; i < int(SortModeCount); i++ {
		if seen[mode] {
			t.Fatalf("mode %v repeated while cycling", mode)
		}
		seen[mode] = true
		if mode.String() == "sort: ?" {
			t.Fatalf("mode %v has no label", mode)
		}
		mode = mode.Next()
	}
	if mode != SortAliasAsc {
		t.Fatalf("cycle did not wrap: ended at %v", mode)
	}
}

// TestLayoutKeepsListHeight guards the View-copies-the-model bug: the list must
// be sized during Update, not at render time, or it only ever shows one row.
func TestLayoutKeepsListHeight(t *testing.T) {
	m := NewLayoutTestApp(120, 32)
	l := m.computeLayout()
	if l.listRows < 20 {
		t.Fatalf("listRows = %d, want a full pane (~29)", l.listRows)
	}
	if l.listInnerW < 20 {
		t.Fatalf("listInnerW = %d, too narrow", l.listInnerW)
	}

	// Searching costs exactly one row (the inline filter field).
	m.searching = true
	searching := m.computeLayout()
	if searching.listRows != l.listRows-1 {
		t.Fatalf("search rows = %d, want %d", searching.listRows, l.listRows-1)
	}
}

// TestOverlayCenterKeepsWidth makes sure overlays never widen the frame.
func TestOverlayCenterKeepsWidth(t *testing.T) {
	base := "abc\nabc\nabc"
	box := "─────\n─hi──\n─────"
	got := overlayCenter(base, box, 20, 3)
	for _, line := range splitLines(got) {
		if w := ansi.StringWidth(line); w != 20 {
			t.Fatalf("line %q has width %d, want 20", line, w)
		}
	}
}

// TestRemotePathHelpers covers POSIX joining for the remote SFTP pane.
func TestRemotePathHelpers(t *testing.T) {
	tests := []struct{ base, name, want string }{
		{"/home/u", "file", "/home/u/file"},
		{"/home/u/", "file", "/home/u/file"},
		{"/", "file", "/file"},
		{"", "file", "/file"},
	}
	for _, tt := range tests {
		if got := remoteJoin(tt.base, tt.name); got != tt.want {
			t.Errorf("remoteJoin(%q,%q) = %q, want %q", tt.base, tt.name, got, tt.want)
		}
	}

	dirs := []struct{ in, want string }{
		{"/home/u/file", "/home/u"},
		{"/home/u", "/home"},
		{"/home", "/"},
		{"/", "/"},
	}
	for _, tt := range dirs {
		if got := remoteDir(tt.in); got != tt.want {
			t.Errorf("remoteDir(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func equalAliases(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- test helpers ----

// NewLayoutTestApp builds a model sized like a real terminal without needing a
// service implementation.
func NewLayoutTestApp(w, h int) AppModel {
	app := NewApp(nil, "test", "test")
	app.width = w
	app.height = h
	return *app
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

// TestApplyLayoutSizesList is the regression guard for the "one visible row"
// bug: SetSize used to be called from View, which runs on a copy of the model.
func TestApplyLayoutSizesList(t *testing.T) {
	app := NewLayoutTestApp(120, 32)
	app.applyLayout()
	if got := app.list.Height(); got < 20 {
		t.Fatalf("list height = %d, want >= 20", got)
	}
	if got := app.list.Width(); got < 20 {
		t.Fatalf("list width = %d, want >= 20", got)
	}

	// Wide terminals get a wider list pane but never more than half the screen.
	wide := NewLayoutTestApp(240, 40)
	l := wide.computeLayout()
	if l.leftW > wide.width/2 {
		t.Fatalf("left pane %d exceeds half of %d", l.leftW, wide.width)
	}
}

// TestThemeCycle ensures the palette can actually be swapped at runtime and that
// every style ends up pointing at the new colours.
func TestThemeCycle(t *testing.T) {
	t.Cleanup(func() { applyTheme(ThemeLight) })

	applyTheme(ThemeLight)
	lightAccent := colorAccent

	applyTheme(ThemeDark)
	if colorAccent == lightAccent {
		t.Fatal("applying the dark theme did not change the accent colour")
	}
	if ThemeLight.Next() != ThemeDark || ThemeDark.Next() != ThemeLight {
		t.Fatal("theme cycle does not toggle between the two themes")
	}
	if ThemeDark.String() == ThemeLight.String() {
		t.Fatal("themes must have distinct labels")
	}

	// Rendering must still produce output after the swap.
	if out := ListSelectedItemStyle.Render("x"); out == "" {
		t.Fatal("styles produced empty output after theme change")
	}

	applyTheme(ThemeLight)
	if colorAccent != lightAccent {
		t.Fatal("restoring the light theme did not restore the accent colour")
	}
}

// TestPaletteFiltersAndRuns covers the command palette: every action must be
// listed, filtering must narrow it, and Enter must invoke the highlighted one.
func TestPaletteFiltersAndRuns(t *testing.T) {
	p := newPaletteState()
	if len(p.filtered) < 15 {
		t.Fatalf("palette only offers %d commands", len(p.filtered))
	}

	// Filtering by label.
	p.query.SetValue("theme")
	p.refilter()
	if len(p.filtered) == 0 || !strings.Contains(strings.ToLower(p.filtered[0].Label), "theme") {
		t.Fatalf("filtering for 'theme' returned %v", p.filtered)
	}

	// Filtering by key hint.
	p.query.SetValue("known")
	p.refilter()
	if len(p.filtered) != 1 || !strings.Contains(p.filtered[0].Label, "known_hosts") {
		t.Fatalf("filtering for 'known' returned %v", p.filtered)
	}

	// A query with no hits must not panic and must leave a usable cursor.
	p.query.SetValue("zzzz-nothing")
	p.refilter()
	if len(p.filtered) != 0 {
		t.Fatalf("expected no matches, got %v", p.filtered)
	}
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", p.cursor)
	}

	// Every command must have a label and a callback.
	for _, c := range paletteCommands() {
		if c.Label == "" || c.Run == nil {
			t.Fatalf("incomplete palette entry: %+v", c)
		}
	}
}

// TestPaletteRunsSelectedCommand drives the palette through a key sequence.
func TestPaletteRunsSelectedCommand(t *testing.T) {
	app := NewLayoutTestApp(120, 32)
	app.state = StatePalette
	app.palette = newPaletteState()
	app.palette.query.SetValue("theme")
	app.palette.refilter()

	before := app.theme
	var model tea.Model = app
	model, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := model.(AppModel)

	if cmd == nil {
		t.Fatal("running the theme command should return a status command")
	}
	if got.theme == before {
		t.Fatal("running the theme command did not toggle the theme")
	}
	if got.state != StateList {
		t.Fatalf("palette did not close, state = %d", got.state)
	}
	applyTheme(ThemeLight)
}

// TestPaletteEscapeCloses makes sure Esc leaves the overlay without side effects.
func TestPaletteEscapeCloses(t *testing.T) {
	app := NewLayoutTestApp(120, 32)
	app.state = StatePalette
	app.palette = newPaletteState()

	var model tea.Model = app
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := model.(AppModel); got.state != StateList {
		t.Fatalf("state = %d, want StateList", got.state)
	}
}

// TestFileBrowserPromptIsVisible covers a silent-failure bug: "n", "r" and "x"
// captured keystrokes into inputValue but nothing drew them, so the keys looked
// like no-ops.
func TestFileBrowserPromptIsVisible(t *testing.T) {
	m := NewFileBrowserModel(domain.Server{Alias: "h", Host: "10.0.0.1"}, stubBrowserService{}, 100, 30)
	m.local.entries = []domain.FileEntry{{Name: "notes.txt"}}
	m.local.cursor = 0

	// mkdir prompt
	m2, _ := m.handleBrowserKey(tea.KeyPressMsg{Code: 'n', Text: "n"}, &[]tea.Cmd{})
	status := m2.renderStatus()
	if !strings.Contains(status, "New directory") {
		t.Fatalf("mkdir prompt not rendered: %q", status)
	}
	m2.inputValue = "demo"
	if got := m2.renderStatus(); !strings.Contains(got, "demo") {
		t.Fatalf("typed text not rendered: %q", got)
	}

	// delete confirmation must not pre-fill the filename, or "y" can never be typed
	m3, _ := m.handleBrowserKey(tea.KeyPressMsg{Code: 'x', Text: "x"}, &[]tea.Cmd{})
	if m3.inputValue != "" {
		t.Fatalf("delete prompt pre-filled the buffer with %q", m3.inputValue)
	}
	if !strings.Contains(m3.renderStatus(), "notes.txt") {
		t.Fatalf("delete prompt should name the target: %q", m3.renderStatus())
	}
	if !strings.Contains(m3.renderStatus(), "type y") {
		t.Fatalf("delete prompt should ask for confirmation: %q", m3.renderStatus())
	}
}

// stubBrowserService satisfies ports.ServerService for UI-only tests.
type stubBrowserService struct{ ports.ServerService }
