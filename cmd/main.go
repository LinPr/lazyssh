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

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/linpr/lazyssh/internal/adapters/data/ssh_config_file"
	"github.com/linpr/lazyssh/internal/adapters/ui"
	"github.com/linpr/lazyssh/internal/core/services"
	"github.com/linpr/lazyssh/internal/logger"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	// Initialize logger
	l, err := logger.New("lazyssh")
	if err != nil {
		log.Fatalf("failed to initialize logger: %v", err)
	}
	defer func() { _ = l.Sync() }()

	// Determine SSH config path
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to determine home directory: %v\n", err)
		os.Exit(1)
	}
	sshConfigPath := filepath.Join(home, ".ssh", "config")
	metaDataPath := filepath.Join(home, ".lazyssh", "metadata.json")

	// Create repository
	repo := ssh_config_file.NewRepository(l, sshConfigPath, metaDataPath)

	// Create service
	svc := services.NewServerService(l, repo)

	// Create app model
	app := ui.NewApp(svc, version, commit)

	// Run the program
	p := tea.NewProgram(app)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error running program: %v\n", err)
		os.Exit(1)
	}
}
