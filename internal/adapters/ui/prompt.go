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
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/linpr/lazyssh/internal/core/domain"
)

// promptAction identifies the follow-up for StateTextPrompt.
const (
	promptNone       = ""
	promptGenKeyPath = "gen-key-path"
)

// promptSubmit runs the side effect attached to a text prompt.
func (m *AppModel) promptSubmit(action, target, value string) tea.Cmd {
	if value == "" {
		return nil
	}
	svc := m.serverService

	switch action {
	case promptGenKeyPath:
		opts := keyGenDefaults(value)
		return func() tea.Msg {
			key, err := svc.GenerateKey(opts)
			if err != nil {
				return StatusMsg{Text: "ssh-keygen: " + err.Error(), Color: colorErrHex}
			}
			return StatusMsg{Text: "created " + key.Path, Color: colorOKHex}
		}
	}
	return nil
}

// homeDir exposes the user's home directory to the UI layer.
func homeDir() (string, error) {
	return os.UserHomeDir()
}

// keyGenDefaults builds generation options from a bare path prompt.
func keyGenDefaults(path string) domain.KeyGenOptions {
	return domain.KeyGenOptions{
		Path:    path,
		Type:    "ed25519",
		Comment: defaultKeyComment(),
	}
}

// defaultKeyComment is user@host, matching ssh-keygen's own default.
func defaultKeyComment() string {
	user := os.Getenv("USER")
	host, _ := os.Hostname()
	if user == "" || host == "" {
		return ""
	}
	return user + "@" + host
}
