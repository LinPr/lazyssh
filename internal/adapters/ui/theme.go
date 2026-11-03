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

import "charm.land/lipgloss/v2"

// Theme is a named colour set. The UI ships a warm light theme (the default)
// and a dark variant that keeps the same accents readable on a dark terminal.
type Theme int

const (
	ThemeLight Theme = iota
	ThemeDark
)

// String is the label shown in the status bar.
func (t Theme) String() string {
	if t == ThemeDark {
		return "theme: dark"
	}
	return "theme: light"
}

// Next cycles between the available themes.
func (t Theme) Next() Theme {
	return (t + 1) % 2
}

// palette holds every colour the interface uses.
type palette struct {
	bg       string
	bgAlt    string
	border   string
	selected string
	text     string
	dim      string
	accent   string
	green    string
	red      string
	yellow   string
	cyan     string
	muted    string
	orange   string
	amber    string
	amberLt  string
}

var lightPalette = palette{
	bg: "#FFFBF5", bgAlt: "#FFF3E0", border: "#F0B429", selected: "#FFE0B2",
	text: "#3E2723", dim: "#8D6E63", accent: "#E65100", green: "#2E7D32",
	red: "#C62828", yellow: "#F57F17", cyan: "#00695C", muted: "#BCAAA4",
	orange: "#E65100", amber: "#FF6F00", amberLt: "#FFB300",
}

var darkPalette = palette{
	bg: "#1B1B1F", bgAlt: "#26262B", border: "#8A7CFF", selected: "#3A3A46",
	text: "#E6E1E5", dim: "#9E9AA7", accent: "#FF9E64", green: "#7BD88F",
	red: "#F7768E", yellow: "#E0AF68", cyan: "#7DCFFF", muted: "#6B6B75",
	orange: "#FF9E64", amber: "#FFB86C", amberLt: "#FFD580",
}

// applyTheme repoints every colour and style at the chosen palette.
//
// Styles are package-level vars, so they can be reassigned at runtime; the next
// render picks them up without restarting the program.
func applyTheme(t Theme) {
	p := lightPalette
	if t == ThemeDark {
		p = darkPalette
	}

	// lipgloss v2 exposes Color as a constructor, so strings are converted here.
	colorBg, colorBgAlt = lipgloss.Color(p.bg), lipgloss.Color(p.bgAlt)
	colorBorder, colorSelected = lipgloss.Color(p.border), lipgloss.Color(p.selected)
	colorText, colorDim, colorAccent = lipgloss.Color(p.text), lipgloss.Color(p.dim), lipgloss.Color(p.accent)
	colorGreen, colorRed = lipgloss.Color(p.green), lipgloss.Color(p.red)
	colorYellow, colorCyan = lipgloss.Color(p.yellow), lipgloss.Color(p.cyan)
	colorMuted, colorOrange = lipgloss.Color(p.muted), lipgloss.Color(p.orange)
	colorAmber, colorAmberLight = lipgloss.Color(p.amber), lipgloss.Color(p.amberLt)

	HeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).
		Background(colorBgAlt).Padding(0, 2).MarginBottom(1)
	ListTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Padding(0, 1)
	ListBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).Padding(0, 1)
	ListItemStyle = lipgloss.NewStyle().Foreground(colorText)
	ListSelectedItemStyle = lipgloss.NewStyle().Bold(true).
		Foreground(colorText).Background(colorSelected)
	DetailsBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).Padding(0, 2)
	StatusBarStyle = lipgloss.NewStyle().Foreground(colorDim).
		Background(colorBgAlt).Padding(0, 1)
	FormBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(colorOrange).Padding(1, 2)
	TabActiveStyle = lipgloss.NewStyle().Bold(true).
		Foreground(colorBg).Background(colorAccent).Padding(0, 2)
	TabInactiveStyle = lipgloss.NewStyle().Foreground(colorDim).
		Background(colorBgAlt).Padding(0, 2)
	HelpStyle = lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	TagStyle = lipgloss.NewStyle().Foreground(colorBg).Background(colorCyan).
		Padding(0, 1).MarginRight(1)
	PinnedStyle = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	KeyStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	PingOnlineStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	PingOfflineStyle = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	PingUnknownStyle = lipgloss.NewStyle().Foreground(colorMuted)
	RawConfigKeywordStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	RawConfigFieldStyle = lipgloss.NewStyle().Foreground(colorText)
	RawConfigValueStyle = lipgloss.NewStyle().Foreground(colorCyan)
	RawConfigCommentStyle = lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	FileBrowserActiveStyle = lipgloss.NewStyle().Bold(true).
		Foreground(colorText).Background(colorSelected).Padding(0, 1)
	FileBrowserInactiveStyle = lipgloss.NewStyle().Foreground(colorText).Padding(0, 1)
	ProgressBarFillStyle = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
	ProgressBarEmptyStyle = lipgloss.NewStyle().Foreground(colorMuted)
	HelpOverlayStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAmber).Padding(1, 2)
	SessionBadgeStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	ForwardBadgeStyle = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	PanelSubtleStyle = lipgloss.NewStyle().Foreground(colorDim)
	PanelHighlightStyle = lipgloss.NewStyle().Bold(true).
		Foreground(colorText).Background(colorSelected)
	PaletteItemStyle = lipgloss.NewStyle().Foreground(colorText)
	// No horizontal padding: an inline highlight must not widen the row, or the
	// selected entry wraps inside the palette box.
	PaletteSelectedStyle = lipgloss.NewStyle().Bold(true).
		Foreground(colorBg).Background(colorAccent)
	PanelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	PanelBorderStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAmber).Padding(1, 2)

	// Keep the table header readable against the current background.
	helpSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
}
