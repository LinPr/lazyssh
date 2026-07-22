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
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/linpr/lazyssh/internal/core/ports"
)

// ---- Message types ----

type RemoteFilesLoadedMsg struct {
	Path    string
	Entries []domain.FileEntry
	Err     error
}

type LocalFilesLoadedMsg struct {
	Path    string
	Entries []domain.FileEntry
	Err     error
}

type TransferProgressMsg struct {
	Progress ports.TransferProgress
}

type FileBrowserExitMsg struct{}

// ---- Panel model ----

type filePanelModel struct {
	title   string
	path    string
	entries []domain.FileEntry
	cursor  int
	offset  int
	height  int
	loading bool
	err     string
}

// ---- FileBrowserModel ----

type FileBrowserModel struct {
	server      domain.Server
	service     ports.ServerService
	local       filePanelModel
	remote      filePanelModel
	activePanel int // 0=local, 1=remote
	inputMode   int // 0=none, 1=mkdir, 2=rename, 3=delete-confirm
	inputValue  string
	transfer    *ports.TransferProgress
	transferDir int // 1=upload, 2=download
	progressCh  chan ports.TransferProgress // active transfer channel for re-queuing
	showHelp    bool
	help        HelpModel
	width       int
	height      int
}

// NewFileBrowserModel creates and initialises a FileBrowserModel.
func NewFileBrowserModel(server domain.Server, svc ports.ServerService, w, h int) FileBrowserModel {
	cwd, _ := os.Getwd()
	panelH := h - 3 // status(1) + border(2)
	if panelH < 3 {
		panelH = 3
	}
	return FileBrowserModel{
		server:  server,
		service: svc,
		local: filePanelModel{
			title:   "LOCAL",
			path:    cwd,
			height:  panelH,
			loading: true,
		},
		remote: filePanelModel{
			title:   "REMOTE",
			path:    ".",
			height:  panelH,
			loading: true,
		},
		help:   NewHelpModel(w, h),
		width:  w,
		height: h,
	}
}

// Init starts loading both panels.
func (m FileBrowserModel) Init() tea.Cmd {
	return tea.Batch(
		loadLocalFiles(m.local.path),
		loadRemoteFiles(m.service, m.server.Alias, m.remote.path),
	)
}

// Update handles messages for the file browser.
func (m FileBrowserModel) Update(msg tea.Msg) (FileBrowserModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		panelH := m.height - 3 // status(1) + border(2)
		if panelH < 3 {
			panelH = 3
		}
		m.local.height = panelH
		m.remote.height = panelH

	case LocalFilesLoadedMsg:
		m.local.loading = false
		if msg.Err != nil {
			m.local.err = msg.Err.Error()
		} else {
			m.local.err = ""
			m.local.entries = msg.Entries
			m.local.path = msg.Path
			m.local.cursor = clampInt(m.local.cursor, 0, max(0, len(m.local.entries)-1))
			m.local.offset = clampInt(m.local.offset, 0, max(0, len(m.local.entries)-1))
		}

	case RemoteFilesLoadedMsg:
		m.remote.loading = false
		if msg.Err != nil {
			m.remote.err = msg.Err.Error()
		} else {
			m.remote.err = ""
			m.remote.entries = msg.Entries
			m.remote.path = msg.Path
			m.remote.cursor = clampInt(m.remote.cursor, 0, max(0, len(m.remote.entries)-1))
			m.remote.offset = clampInt(m.remote.offset, 0, max(0, len(m.remote.entries)-1))
		}

	case TransferProgressMsg:
		p := msg.Progress
		m.transfer = &p
		if p.Done || p.Err != nil {
			m.progressCh = nil
			cmds = append(cmds,
				loadLocalFiles(m.local.path),
				loadRemoteFiles(m.service, m.server.Alias, m.remote.path),
			)
		} else if m.progressCh != nil {
			// Continue reading progress values from the active channel.
			cmds = append(cmds, waitForProgress(m.progressCh))
		}

	case tea.KeyPressMsg:
		if m.inputMode != 0 {
			return m.handleInputKey(msg, &cmds)
		}
		return m.handleBrowserKey(msg, &cmds)
	}

	return m, tea.Batch(cmds...)
}

// handleBrowserKey handles key presses in normal browse mode.
func (m FileBrowserModel) handleBrowserKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (FileBrowserModel, tea.Cmd) {
	panel := &m.local
	if m.activePanel == 1 {
		panel = &m.remote
	}

	switch msg.String() {
	case "tab":
		m.activePanel = 1 - m.activePanel

	case "j", "down":
		if len(panel.entries) == 0 {
			break
		}
		if panel.cursor < len(panel.entries)-1 {
			panel.cursor++
		}
		visibleRows := panel.height - 1
		if visibleRows < 1 {
			visibleRows = 1
		}
		if panel.cursor >= panel.offset+visibleRows {
			panel.offset = panel.cursor - visibleRows + 1
		}

	case "k", "up":
		if len(panel.entries) == 0 {
			break
		}
		if panel.cursor > 0 {
			panel.cursor--
		}
		if panel.cursor < panel.offset {
			panel.offset = panel.cursor
		}

	case "enter":
		if len(panel.entries) > 0 {
			entry := panel.entries[panel.cursor]
			if entry.IsDir {
				newPath := filepath.Join(panel.path, entry.Name)
				if m.activePanel == 0 {
					m.local.loading = true
					*cmds = append(*cmds, loadLocalFiles(newPath))
				} else {
					m.remote.loading = true
					*cmds = append(*cmds, loadRemoteFiles(m.service, m.server.Alias, newPath))
				}
			}
		}

	case "backspace", "h":
		parent := filepath.Dir(panel.path)
		if parent != panel.path {
			if m.activePanel == 0 {
				m.local.loading = true
				*cmds = append(*cmds, loadLocalFiles(parent))
			} else {
				m.remote.loading = true
				*cmds = append(*cmds, loadRemoteFiles(m.service, m.server.Alias, parent))
			}
		}

	case "u":
		// Upload: local -> remote
		if m.activePanel == 0 && len(m.local.entries) > 0 {
			entry := m.local.entries[m.local.cursor]
			localPath := filepath.Join(m.local.path, entry.Name)
			remotePath := m.remote.path + "/" + entry.Name
			progressCh := make(chan ports.TransferProgress, 64)
			m.progressCh = progressCh
			m.transferDir = 1
			svc := m.service
			alias := m.server.Alias
			go func() {
				_ = svc.UploadFile(alias, localPath, remotePath, progressCh)
			}()
			*cmds = append(*cmds, waitForProgress(progressCh))
		}

	case "d":
		// Download: remote -> local
		if m.activePanel == 1 && len(m.remote.entries) > 0 {
			entry := m.remote.entries[m.remote.cursor]
			remotePath := m.remote.path + "/" + entry.Name
			localPath := filepath.Join(m.local.path, entry.Name)
			progressCh := make(chan ports.TransferProgress, 64)
			m.progressCh = progressCh
			m.transferDir = 2
			svc := m.service
			alias := m.server.Alias
			go func() {
				_ = svc.DownloadFile(alias, remotePath, localPath, progressCh)
			}()
			*cmds = append(*cmds, waitForProgress(progressCh))
		}

	case "n":
		m.inputMode = 1
		m.inputValue = ""

	case "x":
		if len(panel.entries) > 0 {
			m.inputMode = 3
			m.inputValue = panel.entries[panel.cursor].Name
		}

	case "r":
		if len(panel.entries) > 0 {
			m.inputMode = 2
			m.inputValue = panel.entries[panel.cursor].Name
		}

	case "R":
		if m.activePanel == 0 {
			m.local.loading = true
			*cmds = append(*cmds, loadLocalFiles(m.local.path))
		} else {
			m.remote.loading = true
			*cmds = append(*cmds, loadRemoteFiles(m.service, m.server.Alias, m.remote.path))
		}

	case "ctrl+c":
		return m, tea.Quit

	case "?":
		m.showHelp = !m.showHelp
		return m, nil

	case "esc", "q":
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		alias := m.server.Alias
		return m, tea.Batch(
			func() tea.Msg { return FileBrowserExitMsg{} },
			func() tea.Msg { return CloseSSHMasterMsg{Alias: alias} },
		)
	}

	return m, tea.Batch(*cmds...)
}

// handleInputKey handles key presses while an input prompt is shown.
func (m FileBrowserModel) handleInputKey(msg tea.KeyPressMsg, cmds *[]tea.Cmd) (FileBrowserModel, tea.Cmd) {
	panel := &m.local
	if m.activePanel == 1 {
		panel = &m.remote
	}

	switch msg.String() {
	case "esc":
		m.inputMode = 0
		m.inputValue = ""

	case "backspace":
		if len(m.inputValue) > 0 {
			m.inputValue = m.inputValue[:len(m.inputValue)-1]
		}

	case "enter":
		switch m.inputMode {
		case 1: // mkdir
			name := strings.TrimSpace(m.inputValue)
			if name != "" {
				newPath := panel.path + "/" + name
				if m.activePanel == 0 {
					if err := os.MkdirAll(newPath, 0750); err == nil {
						*cmds = append(*cmds, loadLocalFiles(panel.path))
					}
				} else {
					alias := m.server.Alias
					svc := m.service
					p := panel.path
					*cmds = append(*cmds, func() tea.Msg {
						err := svc.MkdirRemote(alias, p+"/"+name)
						entries, listErr := svc.ListRemoteFiles(alias, p)
						if listErr != nil {
							return RemoteFilesLoadedMsg{Path: p, Err: listErr}
						}
						_ = err
						return RemoteFilesLoadedMsg{Path: p, Entries: entries}
					})
				}
			}
			m.inputMode = 0
			m.inputValue = ""

		case 2: // rename
			newName := strings.TrimSpace(m.inputValue)
			if newName != "" && len(panel.entries) > 0 {
				oldName := panel.entries[panel.cursor].Name
				if m.activePanel == 0 {
					oldPath := filepath.Join(panel.path, oldName)
					newPath := filepath.Join(panel.path, newName)
					if err := os.Rename(oldPath, newPath); err == nil {
						*cmds = append(*cmds, loadLocalFiles(panel.path))
					}
				} else {
					alias := m.server.Alias
					svc := m.service
					p := panel.path
					*cmds = append(*cmds, func() tea.Msg {
						_ = svc.RenameRemote(alias, p+"/"+oldName, p+"/"+newName)
						entries, err := svc.ListRemoteFiles(alias, p)
						return RemoteFilesLoadedMsg{Path: p, Entries: entries, Err: err}
					})
				}
			}
			m.inputMode = 0
			m.inputValue = ""

		case 3: // delete confirm
			if strings.ToLower(m.inputValue) == "y" && len(panel.entries) > 0 {
				target := panel.entries[panel.cursor].Name
				if m.activePanel == 0 {
					targetPath := filepath.Join(panel.path, target)
					_ = os.RemoveAll(targetPath)
					*cmds = append(*cmds, loadLocalFiles(panel.path))
				} else {
					alias := m.server.Alias
					svc := m.service
					p := panel.path
					*cmds = append(*cmds, func() tea.Msg {
						_ = svc.DeleteRemote(alias, p+"/"+target)
						entries, err := svc.ListRemoteFiles(alias, p)
						return RemoteFilesLoadedMsg{Path: p, Entries: entries, Err: err}
					})
				}
			}
			m.inputMode = 0
			m.inputValue = ""

		default:
			m.inputMode = 0
			m.inputValue = ""
		}

	default:
		// Append printable characters
		key := msg.String()
		if len(key) == 1 {
			m.inputValue += key
		}
	}

	return m, tea.Batch(*cmds...)
}

// ViewString renders the file browser as a string.
func (m FileBrowserModel) ViewString() string {
	panelW := (m.width - 1) / 2 // two panels each half width, -1 for gap
	if panelW < 20 {
		panelW = 20
	}
	panelH := m.height - 3 // status(1) + border(2)
	if panelH < 3 {
		panelH = 3
	}

	leftStr := m.renderPanel(m.local, m.activePanel == 0, panelW, panelH)
	rightStr := m.renderPanel(m.remote, m.activePanel == 1, panelW, panelH)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, leftStr, rightStr)
	statusRow := m.renderStatus()

	base := lipgloss.JoinVertical(lipgloss.Left, panels, statusRow)

	// Overlay help if requested
	if m.showHelp {
		helpBox := m.help.ViewString()
		return overlayCenter(base, helpBox, m.width, m.height)
	}
	return base
}

func (m FileBrowserModel) renderPanel(p filePanelModel, active bool, w, h int) string {
	borderColor := colorBorder
	if active {
		borderColor = colorAccent
	}
	bStyle := lipgloss.NewStyle().Foreground(borderColor)

	inner := w - 2 // visible cols inside left+right border chars
	rowW := inner - 2 // visible cols available for row text (minus 1-col padding each side)
	if rowW < 10 {
		rowW = 10
	}

	var sb strings.Builder

	// Top border: 1 + inner + 1 = w visible cols
	sb.WriteString(bStyle.Render("╭" + strings.Repeat("─", inner) + "╮"))
	sb.WriteString("\n")

	// Title row
	titleIcon := " " // nf-fa-laptop for LOCAL
	if p.title == "REMOTE" {
		titleIcon = " " // nf-fa-server
	}
	titleText := ansi.Truncate(titleIcon+p.title+": "+p.path, rowW, "…")
	titlePad := rowW - ansi.StringWidth(titleText)
	if titlePad < 0 {
		titlePad = 0
	}
	// " " + titleText + padding + " " = 1 + rowW + 1 = inner visible cols
	titleLine := " " + titleText + strings.Repeat(" ", titlePad) + " "
	sb.WriteString(bStyle.Render("│"))
	sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render(titleLine))
	sb.WriteString(bStyle.Render("│"))
	sb.WriteString("\n")

	// writeRow writes | + content padded to inner cols + | + newline.
	writeRow := func(content string) {
		pad := inner - ansi.StringWidth(content)
		if pad > 0 {
			content += strings.Repeat(" ", pad)
		}
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString(content)
		sb.WriteString(bStyle.Render("│"))
		sb.WriteString("\n")
	}

	rowsFilled := 0
	if p.loading {
		msg := ansi.Truncate("  loading...", rowW, "…")
		msgPad := rowW - ansi.StringWidth(msg)
		if msgPad < 0 {
			msgPad = 0
		}
		writeRow(" " + HelpStyle.Render(msg) + strings.Repeat(" ", msgPad) + " ")
		rowsFilled = 1
	} else if p.err != "" {
		msg := ansi.Truncate("  error: "+p.err, rowW, "…")
		msgPad := rowW - ansi.StringWidth(msg)
		if msgPad < 0 {
			msgPad = 0
		}
		writeRow(" " + StatusErrorStyle.Render(msg) + strings.Repeat(" ", msgPad) + " ")
		rowsFilled = 1
	} else {
		end := p.offset + h - 1 // h-1 data rows (title takes 1 row inside the border)
		if end > len(p.entries) {
			end = len(p.entries)
		}
		for i := p.offset; i < end; i++ {
			row := formatFileRow(p.entries[i], rowW)
			var styledRow string
			if i == p.cursor && active && p.cursor < len(p.entries) && i >= p.offset && i < end {
				highlighted := lipgloss.NewStyle().
					Background(colorSelected).
					Foreground(colorText).
					Bold(true).
					Render(row)
				// Re-truncate after applying highlight ANSI codes to guarantee single-line width
				styledRow = " " + ansi.Truncate(highlighted, rowW, "") + " "
			} else {
				styledRow = " " + row + " "
			}
			writeRow(styledRow)
			rowsFilled++
		}
	}

	// Fill remaining rows up to h-1 with blank lines
	for i := rowsFilled; i < h-1; i++ {
		writeRow(strings.Repeat(" ", inner))
	}

	// Bottom border: 1 + inner + 1 = w visible cols (no trailing newline)
	sb.WriteString(bStyle.Render("╰" + strings.Repeat("─", inner) + "╯"))

	return sb.String()
}

// formatFileRow builds a single file row with NerdFont icon, colored name, size, mode, date.
func formatFileRow(e domain.FileEntry, width int) string {
	isSymlink := e.Mode&os.ModeSymlink != 0
	isExec := !e.IsDir && !isSymlink && e.Mode&0111 != 0

	var icon string
	var nameStyle lipgloss.Style
	switch {
	case e.IsDir:
		icon = " " // nf-fa-folder U+F07B
		nameStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	case isSymlink:
		icon = " " // nf-fa-link U+F0C1
		nameStyle = lipgloss.NewStyle().Foreground(colorCyan)
	case isExec:
		icon = " " // nf-oct-terminal U+F489
		nameStyle = lipgloss.NewStyle().Foreground(colorGreen)
	default:
		icon = " " // nf-fa-file U+F016
		nameStyle = lipgloss.NewStyle().Foreground(colorText)
	}

	nameW := 30
	if nameW > width-32 {
		nameW = width - 32
	}
	if nameW < 8 {
		nameW = 8
	}

	name := e.Name
	if len(name) > nameW {
		name = name[:nameW-1] + "~"
	}

	// Pad name first, then apply color so ANSI codes don't break alignment.
	paddedName := fmt.Sprintf("%-*s", nameW, name)
	coloredName := nameStyle.Render(paddedName)

	sizeStr := humanFileSize(e.Size)
	if e.IsDir {
		sizeStr = "      - "
	}

	modeStr := e.Mode.String()
	if len(modeStr) > 10 {
		modeStr = modeStr[:10]
	}

	dateStr := ""
	if !e.ModTime.IsZero() {
		dateStr = e.ModTime.Format("Jan 02 15:04")
	}

	result := fmt.Sprintf("%s %s %8s  %-10s  %12s",
		icon, coloredName, sizeStr, modeStr, dateStr)
	// Truncate to width visible columns to prevent line wrapping
	return ansi.Truncate(result, width, "…")
}

// humanFileSize returns a human-readable size string.
func humanFileSize(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// renderStatus renders the progress bar or a blank status row.
func (m FileBrowserModel) renderStatus() string {
	if m.transfer == nil {
		return HelpStyle.Render(" ")
	}
	p := m.transfer
	if p.Err != nil {
		return StatusErrorStyle.Render("  Transfer error: " + p.Err.Error()) // nf-fa-times U+F00D
	}
	if p.Done {
		return lipgloss.NewStyle().Foreground(colorGreen).Render("  Transfer complete.") // nf-fa-check U+F00C
	}

	dirIcon := " " // nf-fa-upload U+F093
	if m.transferDir == 2 {
		dirIcon = " " // nf-fa-download U+F019
	}

	total := 20
	filled := (p.Percent * total) / 100
	barFill := strings.Repeat("█", filled)
	barEmpty := strings.Repeat("░", total-filled)
	return fmt.Sprintf("%s%s%s  %d%%",
		dirIcon,
		ProgressBarFillStyle.Render(barFill),
		ProgressBarEmptyStyle.Render(barEmpty),
		p.Percent,
	)
}

// clampInt clamps v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---- Commands ----

func loadLocalFiles(path string) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return LocalFilesLoadedMsg{Path: path, Err: err}
		}
		defer f.Close()

		infos, err := f.Readdir(-1)
		if err != nil {
			return LocalFilesLoadedMsg{Path: path, Err: err}
		}

		var dirs []domain.FileEntry
		var files []domain.FileEntry
		for _, info := range infos {
			e := domain.FileEntry{
				Name:    info.Name(),
				IsDir:   info.IsDir(),
				Size:    info.Size(),
				Mode:    info.Mode(),
				ModTime: info.ModTime(),
			}
			if info.IsDir() {
				dirs = append(dirs, e)
			} else {
				files = append(files, e)
			}
		}
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		return LocalFilesLoadedMsg{Path: path, Entries: append(dirs, files...)}
	}
}

func loadRemoteFiles(svc ports.ServerService, alias, path string) tea.Cmd {
	return func() tea.Msg {
		entries, err := svc.ListRemoteFiles(alias, path)
		return RemoteFilesLoadedMsg{Path: path, Entries: entries, Err: err}
	}
}

// waitForProgress reads one progress value from the channel and returns it as a Msg.
func waitForProgress(ch <-chan ports.TransferProgress) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return TransferProgressMsg{Progress: ports.TransferProgress{Done: true}}
		}
		return TransferProgressMsg{Progress: p}
	}
}
