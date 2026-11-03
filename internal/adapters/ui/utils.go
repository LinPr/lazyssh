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
	"strings"

	"github.com/linpr/lazyssh/internal/core/domain"
)

// BuildSSHCommand returns the ssh command string for a server.
// Example: "ssh -p 2222 -i ~/.ssh/id_rsa user@host"
func BuildSSHCommand(s domain.Server) string {
	var parts []string
	parts = append(parts, "ssh")

	if s.Port != 0 && s.Port != 22 {
		parts = append(parts, "-p", fmt.Sprintf("%d", s.Port))
	}

	for _, key := range s.IdentityFiles {
		if key != "" {
			parts = append(parts, "-i", key)
		}
	}

	if s.ProxyJump != "" {
		parts = append(parts, "-J", s.ProxyJump)
	}

	target := s.Host
	if s.User != "" {
		target = s.User + "@" + s.Host
	}
	parts = append(parts, target)

	if s.RemoteCommand != "" {
		parts = append(parts, s.RemoteCommand)
	}

	return strings.Join(parts, " ")
}

// FormatServerTitle returns a single-line title for a list item in the format:
// "alias  host:port  [tag1 tag2]"
func FormatServerTitle(s domain.Server) string {
	alias := s.Alias
	if alias == "" && len(s.Aliases) > 0 {
		alias = s.Aliases[0]
	}

	port := 22
	if s.Port != 0 {
		port = s.Port
	}
	hostPort := fmt.Sprintf("%s:%d", s.Host, port)

	pinned := ""
	if !s.PinnedAt.IsZero() {
		pinned = PinnedStyle.Render("") + " "
	}

	tags := ""
	if len(s.Tags) > 0 {
		var tagParts []string
		for _, t := range s.Tags {
			tagParts = append(tagParts, TagStyle.Render(t))
		}
		tags = "  " + strings.Join(tagParts, "")
	}

	return fmt.Sprintf("%s%s  %s%s", pinned, alias, hostPort, tags)
}

// FormatServerDesc returns a secondary description line shown below the title.
// Example: "user: admin  key: ~/.ssh/id_rsa  connects: 5"
func FormatServerDesc(s domain.Server) string {
	var parts []string

	if s.User != "" {
		parts = append(parts, "user: "+s.User)
	}

	if len(s.IdentityFiles) > 0 && s.IdentityFiles[0] != "" {
		parts = append(parts, "key: "+s.IdentityFiles[0])
	}

	if s.SSHCount > 0 {
		parts = append(parts, fmt.Sprintf("connects: %d", s.SSHCount))
	}

	if !s.LastSeen.IsZero() {
		parts = append(parts, "last: "+s.LastSeen.Format("2006-01-02"))
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "  ")
}

// FormatServerDetails returns the full multi-line details view for a server.
func FormatServerDetails(s domain.Server) string {
	var sb strings.Builder

	label := func(k, v string) {
		if v == "" {
			return
		}
		sb.WriteString(KeyStyle.Render(k+":") + "  " + v + "\n")
	}

	alias := s.Alias
	if alias == "" && len(s.Aliases) > 0 {
		alias = strings.Join(s.Aliases, ", ")
	}
	label("Alias", alias)
	label("Host", s.Host)

	port := 22
	if s.Port != 0 {
		port = s.Port
	}
	label("Port", fmt.Sprintf("%d", port))
	label("User", s.User)

	if len(s.IdentityFiles) > 0 {
		label("Identity Files", strings.Join(s.IdentityFiles, ", "))
	}
	if len(s.Tags) > 0 {
		label("Tags", strings.Join(s.Tags, ", "))
	}

	if s.SSHCount > 0 {
		label("SSH Count", fmt.Sprintf("%d", s.SSHCount))
	}
	if !s.LastSeen.IsZero() {
		label("Last Seen", s.LastSeen.Format("2006-01-02 15:04"))
	}
	if !s.PinnedAt.IsZero() {
		label("Pinned At", s.PinnedAt.Format("2006-01-02 15:04"))
	}

	// Connection settings
	label("ProxyJump", s.ProxyJump)
	label("ProxyCommand", s.ProxyCommand)
	label("RemoteCommand", s.RemoteCommand)
	label("ConnectTimeout", s.ConnectTimeout)
	label("ControlMaster", s.ControlMaster)
	label("ControlPath", s.ControlPath)
	label("ControlPersist", s.ControlPersist)

	// Authentication
	label("PubkeyAuthentication", s.PubkeyAuthentication)
	label("IdentitiesOnly", s.IdentitiesOnly)
	label("ForwardAgent", s.ForwardAgent)
	label("AddKeysToAgent", s.AddKeysToAgent)

	// Reliability
	label("ServerAliveInterval", s.ServerAliveInterval)
	label("ServerAliveCountMax", s.ServerAliveCountMax)
	label("Compression", s.Compression)
	label("TCPKeepAlive", s.TCPKeepAlive)

	// Security
	label("StrictHostKeyChecking", s.StrictHostKeyChecking)
	label("UserKnownHostsFile", s.UserKnownHostsFile)

	// Port forwarding
	if len(s.LocalForward) > 0 {
		label("LocalForward", strings.Join(s.LocalForward, ", "))
	}
	if len(s.RemoteForward) > 0 {
		label("RemoteForward", strings.Join(s.RemoteForward, ", "))
	}
	if len(s.DynamicForward) > 0 {
		label("DynamicForward", strings.Join(s.DynamicForward, ", "))
	}

	sb.WriteString("\n" + HelpStyle.Render("SSH: "+BuildSSHCommand(s)))

	return sb.String()
}

// GetAvailableSSHKeys returns a list of private key file paths found in ~/.ssh/.
// It skips .pub files, known_hosts, authorized_keys, config, and directories.
func GetAvailableSSHKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return nil
	}

	skip := map[string]bool{
		"known_hosts":      true,
		"known_hosts.old":  true,
		"authorized_keys":  true,
		"authorized_keys2": true,
		"config":           true,
	}

	var keys []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".pub") {
			continue
		}
		if skip[name] {
			continue
		}
		keys = append(keys, filepath.Join(sshDir, name))
	}
	return keys
}
