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

// layout holds every size derived from the terminal dimensions.
//
// It is recomputed in Update (on WindowSizeMsg and whenever the search box
// appears/disappears) instead of inside View, because View runs on a copy of the
// model: calling list.SetSize there mutated a throw-away value and left the real
// model with height 0, so only one row was ever rendered.
type layout struct {
	width, height int
	bodyH         int // rows available above the status bar
	leftW         int // outer width of the left pane
	rightW        int
	listInnerW    int // usable text columns inside the left pane border+padding
	listRows      int // rows handed to the list component
}

const (
	// minLeftPane keeps aliases readable on narrow terminals.
	minLeftPane = 22
	// statusBarRows is the height reserved at the bottom of the screen.
	statusBarRows = 1
)

func (m *AppModel) computeLayout() layout {
	l := layout{width: m.width, height: m.height}
	if l.width <= 0 || l.height <= 0 {
		return l
	}

	l.bodyH = l.height - statusBarRows
	if l.bodyH < 3 {
		l.bodyH = 3
	}

	l.leftW = l.width / 4
	if l.leftW < minLeftPane {
		l.leftW = minLeftPane
	}
	if l.leftW > l.width-20 {
		l.leftW = max(20, l.width/2)
	}
	l.rightW = l.width - l.leftW

	// Left pane box: 1 column border each side + 1 column padding each side.
	l.listInnerW = l.leftW - 4
	if l.listInnerW < 8 {
		l.listInnerW = 8
	}

	// Left pane rows: subtract the top/bottom border, plus one more row for the
	// inline search field when searching.
	l.listRows = l.bodyH - 2
	if m.searching {
		l.listRows--
	}
	// The list widget renders its own title line, which the border already
	// accounts for visually; keep one spare row so it never clips.
	l.listRows--
	if l.listRows < 1 {
		l.listRows = 1
	}

	return l
}

// applyLayout pushes the computed sizes into the components that cache them.
func (m *AppModel) applyLayout() {
	l := m.computeLayout()
	if l.width <= 0 {
		return
	}
	m.list.SetSize(l.listInnerW, l.listRows)
	m.details.SetSize(l.rightW, l.bodyH)
}

// lastLayout is a convenience for View, which must mirror what Update configured.
func (m *AppModel) lastLayout() layout {
	return m.computeLayout()
}
