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

// Color palette (Warm Light theme)
var (
	colorBg       = lipgloss.Color("#FFFBF5")
	colorBgAlt    = lipgloss.Color("#FFF3E0")
	colorBorder   = lipgloss.Color("#F0B429")
	colorSelected = lipgloss.Color("#FFE0B2")
	colorText     = lipgloss.Color("#3E2723")
	colorDim      = lipgloss.Color("#8D6E63")
	colorAccent   = lipgloss.Color("#E65100")
	colorGreen    = lipgloss.Color("#2E7D32")
	colorRed      = lipgloss.Color("#C62828")
	colorYellow   = lipgloss.Color("#F57F17")
	colorCyan     = lipgloss.Color("#00695C")
	colorMuted    = lipgloss.Color("#BCAAA4")
	// Warm accent colors for form overlay
	colorOrange     = lipgloss.Color("#E65100")
	colorAmber      = lipgloss.Color("#FF6F00")
	colorAmberLight = lipgloss.Color("#FFB300")
)

// HeaderStyle is the top application title bar.
var HeaderStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(colorAccent).
	Background(colorBgAlt).
	Padding(0, 2).
	MarginBottom(1)

// ListTitleStyle is the title shown above the server list pane.
var ListTitleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(colorCyan).
	Padding(0, 1)

// ListBorderStyle wraps the server list pane with a rounded border.
var ListBorderStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorBorder).
	Padding(0, 1)

// ListItemStyle is the default (unselected) list row.
var ListItemStyle = lipgloss.NewStyle().
	Foreground(colorText).
	Padding(0, 1)

// ListSelectedItemStyle highlights the currently focused list row.
var ListSelectedItemStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(colorText).
	Background(colorSelected).
	Padding(0, 1)

// DetailsBorderStyle wraps the server detail pane with a rounded border.
// Uses Padding(0, 2) — horizontal padding only — so vertical overhead matches
// ListBorderStyle (border=2, no vertical padding) and both panels stay equal height.
var DetailsBorderStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorAccent).
	Padding(0, 2)

// StatusBarStyle is the bottom status / help bar.
var StatusBarStyle = lipgloss.NewStyle().
	Foreground(colorDim).
	Background(colorBgAlt).
	Padding(0, 1)

// StatusSuccessStyle is used for success messages in the status bar.
var StatusSuccessStyle = lipgloss.NewStyle().
	Foreground(colorGreen).
	Background(colorBgAlt).
	Bold(true).
	Padding(0, 1)

// StatusErrorStyle is used for error messages in the status bar.
var StatusErrorStyle = lipgloss.NewStyle().
	Foreground(colorRed).
	Background(colorBgAlt).
	Bold(true).
	Padding(0, 1)

// FormBorderStyle wraps modal forms (add / edit) with a rounded border in amber/orange.
var FormBorderStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorOrange).
	Padding(1, 2)

// TabActiveStyle renders the currently selected tab.
var TabActiveStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(colorBg).
	Background(colorAccent).
	Padding(0, 2)

// TabInactiveStyle renders non-selected tabs.
var TabInactiveStyle = lipgloss.NewStyle().
	Foreground(colorDim).
	Background(colorBgAlt).
	Padding(0, 2)

// HelpStyle renders keyboard shortcut hints.
var HelpStyle = lipgloss.NewStyle().
	Foreground(colorDim).
	Italic(true)

// TagStyle renders server tag badges.
var TagStyle = lipgloss.NewStyle().
	Foreground(colorBg).
	Background(colorCyan).
	Padding(0, 1).
	MarginRight(1)

// PinnedStyle renders the pinned-server indicator.
var PinnedStyle = lipgloss.NewStyle().
	Foreground(colorYellow).
	Bold(true)

// KeyStyle renders key names inside help text (e.g. "q", "Enter").
var KeyStyle = lipgloss.NewStyle().
	Foreground(colorAccent).
	Bold(true)

// PingOnlineStyle renders a bright green filled circle for online ping status.
var PingOnlineStyle = lipgloss.NewStyle().
	Foreground(colorGreen).
	Bold(true)

// PingOfflineStyle renders a red filled circle for offline ping status.
var PingOfflineStyle = lipgloss.NewStyle().
	Foreground(colorRed).
	Bold(true)

// PingUnknownStyle renders a gray dim dot for unknown ping status.
var PingUnknownStyle = lipgloss.NewStyle().
	Foreground(colorMuted)

// RawConfigKeywordStyle renders Host/Match keywords in accent blue bold.
var RawConfigKeywordStyle = lipgloss.NewStyle().
	Foreground(colorAccent).
	Bold(true)

// RawConfigFieldStyle renders SSH config parameter names in normal text.
var RawConfigFieldStyle = lipgloss.NewStyle().
	Foreground(colorText)

// RawConfigValueStyle renders SSH config parameter values in cyan.
var RawConfigValueStyle = lipgloss.NewStyle().
	Foreground(colorCyan)

// RawConfigCommentStyle renders # comments in dim italic.
var RawConfigCommentStyle = lipgloss.NewStyle().
	Foreground(colorDim).
	Italic(true)

// FileBrowserActiveStyle renders the selected item in the file browser.
var FileBrowserActiveStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(colorText).
	Background(colorSelected).
	Padding(0, 1)

// FileBrowserInactiveStyle renders normal (unselected) items in the file browser.
var FileBrowserInactiveStyle = lipgloss.NewStyle().
	Foreground(colorText).
	Padding(0, 1)

// ProgressBarFillStyle renders the filled portion of a progress bar in blue.
var ProgressBarFillStyle = lipgloss.NewStyle().
	Foreground(colorOrange).
	Bold(true)

// ProgressBarEmptyStyle renders the empty portion of a progress bar in dim.
var ProgressBarEmptyStyle = lipgloss.NewStyle().
	Foreground(colorMuted)

// HelpOverlayStyle renders the border for the help overlay modal.
var HelpOverlayStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colorAmber).
	Padding(1, 2)
