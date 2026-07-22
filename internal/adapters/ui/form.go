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

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/linpr/lazyssh/internal/core/domain"
)

// FormMode indicates whether the form is adding or editing a server.
type FormMode int

const (
	FormAdd  FormMode = iota
	FormEdit
)

// FormTab groups a set of fields under a named tab.
type FormTab struct {
	name   string
	fields []string // field names in order
}

// FormModel is the Bubble Tea model for the server add/edit form.
type FormModel struct {
	mode       FormMode
	original   *domain.Server
	tabs       []FormTab
	currentTab int
	inputs     map[string]textinput.Model // field name -> input
	focusIdx   int                        // index within current tab's fields
	width      int
	height     int
	err        string
}

// FormSaveMsg is emitted when the user confirms the form.
type FormSaveMsg struct {
	Server   domain.Server
	Original *domain.Server
}

// FormSavePreviewMsg is emitted when Enter is pressed on the form.
// It carries the validated server, original for edit mode, and a preview of the SSH config block.
type FormSavePreviewMsg struct {
	Server   domain.Server
	Original *domain.Server
	Preview  string
}

// FormCancelMsg is emitted when the user cancels the form.
type FormCancelMsg struct{}

// formTabs defines the five tabs and their field order.
var formTabs = []FormTab{
	{
		name:   "Basic",
		fields: []string{"Alias", "Host", "User", "Port", "Keys", "Tags"},
	},
	{
		name: "Connection",
		fields: []string{
			"ProxyJump", "ProxyCommand",
			"ConnectTimeout", "ConnectionAttempts",
			"ServerAliveInterval", "ServerAliveCountMax",
			"Compression", "ControlMaster", "ControlPath", "ControlPersist",
		},
	},
	{
		name:   "Forwarding",
		fields: []string{"LocalForward", "RemoteForward", "DynamicForward"},
	},
	{
		name: "Authentication",
		fields: []string{
			"PubkeyAuthentication", "IdentitiesOnly",
			"PasswordAuthentication", "PreferredAuthentications", "AddKeysToAgent",
		},
	},
	{
		name: "Advanced",
		fields: []string{
			"StrictHostKeyChecking", "Ciphers", "MACs",
			"KexAlgorithms", "HostKeyAlgorithms", "LogLevel",
		},
	},
}

// formPlaceholders provides example text for each input field.
var formPlaceholders = map[string]string{
	"Alias":                    "e.g. myserver",
	"Host":                     "e.g. 192.168.1.1 or example.com",
	"User":                     "e.g. ubuntu",
	"Port":                     "22",
	"Keys":                     "e.g. ~/.ssh/id_ed25519",
	"Tags":                     "e.g. prod, web",
	"ProxyJump":                "e.g. user@jumphost",
	"ProxyCommand":             "e.g. ssh -W %h:%p jumphost",
	"ConnectTimeout":           "seconds, e.g. 10",
	"ConnectionAttempts":       "e.g. 3",
	"ServerAliveInterval":      "seconds, e.g. 60",
	"ServerAliveCountMax":      "e.g. 3",
	"Compression":              "yes / no",
	"ControlMaster":            "yes / no / auto / ask / autoask",
	"ControlPath":              "e.g. ~/.ssh/cm-%r@%h:%p",
	"ControlPersist":           "yes / no / seconds",
	"LocalForward":             "e.g. 8080:localhost:80",
	"RemoteForward":            "e.g. 9090:localhost:9090",
	"DynamicForward":           "e.g. 1080",
	"PubkeyAuthentication":     "yes / no",
	"IdentitiesOnly":           "yes / no",
	"PasswordAuthentication":   "yes / no",
	"PreferredAuthentications": "e.g. publickey,password",
	"AddKeysToAgent":           "yes / no / ask / confirm",
	"StrictHostKeyChecking":    "yes / no / ask / accept-new",
	"Ciphers":                  "comma-separated cipher list",
	"MACs":                     "comma-separated MAC list",
	"KexAlgorithms":            "comma-separated KEX list",
	"HostKeyAlgorithms":        "comma-separated host-key list",
	"LogLevel":                 "QUIET/FATAL/ERROR/INFO/VERBOSE/DEBUG",
}

// form-specific styles that complement styles.go
var (
	formLabelStyle        = lipgloss.NewStyle().Foreground(colorAmber).Width(26)
	formFocusedLabelStyle = lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Width(26)
	formTabSepStyle       = lipgloss.NewStyle().Foreground(colorMuted)
	formHintStyle         = lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	formErrStyle          = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	formTitleStyle        = lipgloss.NewStyle().Bold(true).Foreground(colorAmberLight)
)

// newFormInput creates a textinput with the placeholder for the given field.
func newFormInput(fieldName string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = formPlaceholders[fieldName]
	ti.CharLimit = 512
	ti.SetWidth(40)
	return ti
}

// initFormInputs builds the inputs map, pre-populating values in edit mode.
func initFormInputs(mode FormMode, original *domain.Server) map[string]textinput.Model {
	inputs := make(map[string]textinput.Model)

	// Collect every field name from all tabs.
	var allFields []string
	for _, tab := range formTabs {
		allFields = append(allFields, tab.fields...)
	}

	for _, f := range allFields {
		ti := newFormInput(f)
		ti.SetValue(fieldValue(f, mode, original))
		inputs[f] = ti
	}
	return inputs
}

// fieldValue returns the initial value for a field, sourced from the original
// server when in edit mode.
func fieldValue(field string, mode FormMode, original *domain.Server) string {
	if mode != FormEdit || original == nil {
		if field == "Port" {
			return "22"
		}
		return ""
	}
	switch field {
	case "Alias":
		return original.Alias
	case "Host":
		return original.Host
	case "User":
		return original.User
	case "Port":
		if original.Port > 0 {
			return strconv.Itoa(original.Port)
		}
		return "22"
	case "Keys":
		return strings.Join(original.IdentityFiles, ", ")
	case "Tags":
		return strings.Join(original.Tags, ", ")
	case "ProxyJump":
		return original.ProxyJump
	case "ProxyCommand":
		return original.ProxyCommand
	case "ConnectTimeout":
		return original.ConnectTimeout
	case "ConnectionAttempts":
		return original.ConnectionAttempts
	case "ServerAliveInterval":
		return original.ServerAliveInterval
	case "ServerAliveCountMax":
		return original.ServerAliveCountMax
	case "Compression":
		return original.Compression
	case "ControlMaster":
		return original.ControlMaster
	case "ControlPath":
		return original.ControlPath
	case "ControlPersist":
		return original.ControlPersist
	case "LocalForward":
		return strings.Join(original.LocalForward, ", ")
	case "RemoteForward":
		return strings.Join(original.RemoteForward, ", ")
	case "DynamicForward":
		return strings.Join(original.DynamicForward, ", ")
	case "PubkeyAuthentication":
		return original.PubkeyAuthentication
	case "IdentitiesOnly":
		return original.IdentitiesOnly
	case "PasswordAuthentication":
		return original.PasswordAuthentication
	case "PreferredAuthentications":
		return original.PreferredAuthentications
	case "AddKeysToAgent":
		return original.AddKeysToAgent
	case "StrictHostKeyChecking":
		return original.StrictHostKeyChecking
	case "Ciphers":
		return original.Ciphers
	case "MACs":
		return original.MACs
	case "KexAlgorithms":
		return original.KexAlgorithms
	case "HostKeyAlgorithms":
		return original.HostKeyAlgorithms
	case "LogLevel":
		return original.LogLevel
	}
	return ""
}

// NewFormModel creates a new form model for adding or editing a server.
func NewFormModel(mode FormMode, original *domain.Server) FormModel {
	inputs := initFormInputs(mode, original)
	m := FormModel{
		mode:       mode,
		original:   original,
		tabs:       formTabs,
		currentTab: 0,
		inputs:     inputs,
		focusIdx:   0,
		width:      80,
		height:     24,
	}

	// Focus first field of first tab
	if len(formTabs) > 0 && len(formTabs[0].fields) > 0 {
		firstField := formTabs[0].fields[0]
		ti := m.inputs[firstField]
		ti.Focus()
		m.inputs[firstField] = ti
	}

	return m
}

// Init implements tea.Model.
func (m FormModel) Init() tea.Cmd {
	return textinput.Blink
}

// currentFields returns the field list for the currently active tab.
func (m FormModel) currentFields() []string {
	if m.currentTab < len(m.tabs) {
		return m.tabs[m.currentTab].fields
	}
	return nil
}

// focusedField returns the currently focused field name.
func (m FormModel) focusedField() string {
	fields := m.currentFields()
	if m.focusIdx >= 0 && m.focusIdx < len(fields) {
		return fields[m.focusIdx]
	}
	return ""
}

// setFocus moves focus to the input at newIdx within the current tab.
func (m FormModel) setFocus(newIdx int) FormModel {
	fields := m.currentFields()
	if len(fields) == 0 {
		return m
	}

	// Blur previous
	prev := m.focusedField()
	if prev != "" {
		ti := m.inputs[prev]
		ti.Blur()
		m.inputs[prev] = ti
	}

	// Wrap newIdx
	if newIdx < 0 {
		newIdx = len(fields) - 1
	} else if newIdx >= len(fields) {
		newIdx = 0
	}
	m.focusIdx = newIdx

	// Focus new
	next := fields[newIdx]
	ti := m.inputs[next]
	ti.Focus()
	m.inputs[next] = ti

	return m
}

// switchTab moves to a different tab by delta (-1 or +1).
func (m FormModel) switchTab(delta int) FormModel {
	// Blur current focused input
	prev := m.focusedField()
	if prev != "" {
		ti := m.inputs[prev]
		ti.Blur()
		m.inputs[prev] = ti
	}

	// Move to new tab
	m.currentTab = (m.currentTab + delta + len(m.tabs)) % len(m.tabs)
	m.focusIdx = 0

	// Focus first field of new tab
	fields := m.currentFields()
	if len(fields) > 0 {
		ti := m.inputs[fields[0]]
		ti.Focus()
		m.inputs[fields[0]] = ti
	}

	return m
}

// Update implements tea.Model.
func (m FormModel) Update(msg tea.Msg) (FormModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return FormCancelMsg{} }

		case "enter":
			ok, errMsg := m.IsValid()
			if !ok {
				m.err = errMsg
				return m, nil
			}
			m.err = ""
			server := m.ToServer()
			preview := buildSSHConfigPreview(server)
			return m, func() tea.Msg {
				return FormSavePreviewMsg{Server: server, Original: m.original, Preview: preview}
			}

		case "tab":
			m = m.switchTab(1)
			return m, textinput.Blink

		case "shift+tab":
			m = m.switchTab(-1)
			return m, textinput.Blink

		case "up", "k":
			m = m.setFocus(m.focusIdx - 1)
			return m, textinput.Blink

		case "down", "j":
			m = m.setFocus(m.focusIdx + 1)
			return m, textinput.Blink
		}
	}

	// Update the currently focused input
	focused := m.focusedField()
	if focused != "" {
		ti, cmd := m.inputs[focused].Update(msg)
		m.inputs[focused] = ti
		return m, cmd
	}

	return m, nil
}

// View implements tea.Model.
func (m FormModel) View() tea.View {
	var sb strings.Builder

	// Title
	title := "Add Server"
	if m.mode == FormEdit {
		title = "Edit Server"
	}
	sb.WriteString(formTitleStyle.Render(title))
	sb.WriteString("\n\n")

	// Tab bar
	var tabParts []string
	for i, tab := range m.tabs {
		if i == m.currentTab {
			tabParts = append(tabParts, TabActiveStyle.Render(tab.name))
		} else {
			tabParts = append(tabParts, TabInactiveStyle.Render(tab.name))
		}
	}
	sb.WriteString(strings.Join(tabParts, formTabSepStyle.Render(" | ")))
	sb.WriteString("\n\n")

	// Fields for current tab
	fields := m.currentFields()
	for i, fieldName := range fields {
		ti := m.inputs[fieldName]
		isFocused := i == m.focusIdx

		var lbl string
		if isFocused {
			lbl = formFocusedLabelStyle.Render(fieldName + ":")
		} else {
			lbl = formLabelStyle.Render(fieldName + ":")
		}

		sb.WriteString(lbl + " " + ti.View())
		sb.WriteString("\n")
	}

	sb.WriteString("\n")

	// Error message
	if m.err != "" {
		sb.WriteString(formErrStyle.Render("✗ " + m.err))
		sb.WriteString("\n\n")
	}

	// Hints
	sb.WriteString(formHintStyle.Render(
		"Tab/Shift+Tab: next/prev tab  •  Up/Down (k/j): prev/next field  •  Enter: preview  •  Esc: cancel",
	))

	return tea.NewView(FormBorderStyle.Render(sb.String()))
}

// ToServer converts the form input into a domain.Server.
func (m FormModel) ToServer() domain.Server {
	get := func(name string) string {
		if ti, ok := m.inputs[name]; ok {
			return strings.TrimSpace(ti.Value())
		}
		return ""
	}

	splitComma := func(s string) []string {
		if s == "" {
			return nil
		}
		var result []string
		for _, item := range strings.Split(s, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}

	port := 22
	if p, err := strconv.Atoi(get("Port")); err == nil && p > 0 {
		port = p
	}

	server := domain.Server{
		Alias:                    get("Alias"),
		Host:                     get("Host"),
		User:                     get("User"),
		Port:                     port,
		IdentityFiles:            splitComma(get("Keys")),
		Tags:                     splitComma(get("Tags")),
		ProxyJump:                get("ProxyJump"),
		ProxyCommand:             get("ProxyCommand"),
		ConnectTimeout:           get("ConnectTimeout"),
		ConnectionAttempts:       get("ConnectionAttempts"),
		ServerAliveInterval:      get("ServerAliveInterval"),
		ServerAliveCountMax:      get("ServerAliveCountMax"),
		Compression:              get("Compression"),
		ControlMaster:            get("ControlMaster"),
		ControlPath:              get("ControlPath"),
		ControlPersist:           get("ControlPersist"),
		LocalForward:             splitComma(get("LocalForward")),
		RemoteForward:            splitComma(get("RemoteForward")),
		DynamicForward:           splitComma(get("DynamicForward")),
		PubkeyAuthentication:     get("PubkeyAuthentication"),
		IdentitiesOnly:           get("IdentitiesOnly"),
		PasswordAuthentication:   get("PasswordAuthentication"),
		PreferredAuthentications: get("PreferredAuthentications"),
		AddKeysToAgent:           get("AddKeysToAgent"),
		StrictHostKeyChecking:    get("StrictHostKeyChecking"),
		Ciphers:                  get("Ciphers"),
		MACs:                     get("MACs"),
		KexAlgorithms:            get("KexAlgorithms"),
		HostKeyAlgorithms:        get("HostKeyAlgorithms"),
		LogLevel:                 get("LogLevel"),
	}

	// Preserve metadata fields when editing
	if m.mode == FormEdit && m.original != nil {
		server.PinnedAt = m.original.PinnedAt
		server.LastSeen = m.original.LastSeen
		server.SSHCount = m.original.SSHCount
		server.Aliases = m.original.Aliases
	}

	return server
}

// IsValid checks that required fields are filled and returns (ok, errorMessage).
func (m FormModel) IsValid() (bool, string) {
	get := func(name string) string {
		if ti, ok := m.inputs[name]; ok {
			return strings.TrimSpace(ti.Value())
		}
		return ""
	}

	if get("Alias") == "" {
		return false, "Alias is required"
	}
	if get("Host") == "" {
		return false, "Host is required"
	}

	portStr := get("Port")
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return false, fmt.Sprintf("Port must be between 1 and 65535, got %q", portStr)
		}
	}

	return true, ""
}

// buildSSHConfigPreview builds a preview string in SSH config block format from a domain.Server.
func buildSSHConfigPreview(server domain.Server) string {
	var sb strings.Builder
	sb.WriteString("Host " + server.Alias + "\n")

	write := func(key, value string) {
		if value != "" {
			sb.WriteString("    " + key + " " + value + "\n")
		}
	}

	write("HostName", server.Host)
	write("User", server.User)
	if server.Port > 0 {
		write("Port", strconv.Itoa(server.Port))
	}
	for _, idf := range server.IdentityFiles {
		write("IdentityFile", idf)
	}
	write("ProxyJump", server.ProxyJump)
	write("ProxyCommand", server.ProxyCommand)
	write("ConnectTimeout", server.ConnectTimeout)
	write("ConnectionAttempts", server.ConnectionAttempts)
	write("ServerAliveInterval", server.ServerAliveInterval)
	write("ServerAliveCountMax", server.ServerAliveCountMax)
	write("Compression", server.Compression)
	write("ControlMaster", server.ControlMaster)
	write("ControlPath", server.ControlPath)
	write("ControlPersist", server.ControlPersist)
	for _, lf := range server.LocalForward {
		write("LocalForward", lf)
	}
	for _, rf := range server.RemoteForward {
		write("RemoteForward", rf)
	}
	for _, df := range server.DynamicForward {
		write("DynamicForward", df)
	}
	write("PubkeyAuthentication", server.PubkeyAuthentication)
	write("IdentitiesOnly", server.IdentitiesOnly)
	write("PasswordAuthentication", server.PasswordAuthentication)
	write("PreferredAuthentications", server.PreferredAuthentications)
	write("AddKeysToAgent", server.AddKeysToAgent)
	write("StrictHostKeyChecking", server.StrictHostKeyChecking)
	write("Ciphers", server.Ciphers)
	write("MACs", server.MACs)
	write("KexAlgorithms", server.KexAlgorithms)
	write("HostKeyAlgorithms", server.HostKeyAlgorithms)
	write("LogLevel", server.LogLevel)

	return sb.String()
}

