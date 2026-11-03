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
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/linpr/lazyssh/internal/adapters/data/ssh_config_file"
	"github.com/linpr/lazyssh/internal/adapters/ui"
	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/linpr/lazyssh/internal/core/services"
	"github.com/linpr/lazyssh/internal/logger"
)

var (
	version = "dev"
	commit  = "none"
)

const usage = `lazyssh — a TUI for ~/.ssh/config

usage:
  lazyssh                 launch the TUI
  lazyssh <alias>         connect directly with ssh <alias>
  lazyssh list            print configured hosts (tab separated)
  lazyssh check           validate ~/.ssh/config and exit non-zero on problems
  lazyssh doctor          print environment diagnostics
  lazyssh --version, -v   print version and commit

options:
  --config <path>         use an ssh config other than ~/.ssh/config
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "lazyssh: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	args, configPath, showVersion, err := parseArgs(os.Args[1:])
	if err != nil {
		return err
	}
	if showVersion {
		fmt.Printf("lazyssh %s (%s)\n", version, commit)
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to determine home directory: %w", err)
	}
	if configPath == "" {
		configPath = filepath.Join(home, ".ssh", "config")
	}
	metaDataPath := filepath.Join(home, ".lazyssh", "metadata.json")

	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Print(usage)
			return nil
		}
	}

	// Non-interactive commands still need a logger, but writing it should never
	// be a hard failure (e.g. read-only home).
	l, err := logger.New("lazyssh")
	if err != nil {
		log.Printf("lazyssh: logger unavailable, continuing without file logs: %v", err)
		l = logger.Discard()
	}
	defer func() { _ = l.Sync() }()

	repo := ssh_config_file.NewRepository(l, configPath, metaDataPath)
	svc := services.NewServerService(l, repo, configPath)

	if len(args) > 0 {
		switch args[0] {
		case "list":
			return runList(svc)
		case "check":
			return runCheck(svc)
		case "doctor":
			return runDoctor(svc, configPath, metaDataPath, l)
		default:
			// Treat a bare argument as an alias to connect to.
			return runConnect(svc, args[0])
		}
	}

	app := ui.NewApp(svc, version, commit)
	if _, err := tea.NewProgram(app).Run(); err != nil {
		return fmt.Errorf("error running program: %w", err)
	}
	return nil
}

// parseArgs extracts flags while keeping positional arguments in order.
func parseArgs(argv []string) (rest []string, configPath string, showVersion bool, err error) {
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--version", "-v":
			showVersion = true
		case "--config", "-c":
			if i+1 >= len(argv) {
				return nil, "", false, fmt.Errorf("--config needs a path")
			}
			configPath = argv[i+1]
			i++
		case "--help", "-h":
			rest = append(rest, "help")
		default:
			rest = append(rest, argv[i])
		}
	}
	return rest, configPath, showVersion, nil
}

// runConnect execs ssh for a single alias, keeping the terminal attached.
func runConnect(svc interface {
	SSH(alias string) error
}, alias string) error {
	if err := svc.SSH(alias); err != nil {
		return fmt.Errorf("ssh %s: %w", alias, err)
	}
	return nil
}

// runList prints the configured hosts for scripting.
func runList(svc interface {
	ListServers(string) ([]domain.Server, error)
}) error {
	servers, err := svc.ListServers("")
	if err != nil {
		return err
	}
	for _, s := range servers {
		fmt.Printf("%s\t%s\t%d\t%s\n", s.Alias, s.Host, s.Port, s.User)
	}
	return nil
}

// runCheck validates the config, printing every problem it finds.
func runCheck(svc interface {
	ValidateConfig() ([]string, error)
}) error {
	problems, err := svc.ValidateConfig()
	if err != nil {
		return err
	}
	if len(problems) == 0 {
		fmt.Println("ok: ssh config looks valid")
		return nil
	}
	for _, p := range problems {
		fmt.Println("warn:", p)
	}
	return fmt.Errorf("%d problem(s) found", len(problems))
}

// runDoctor prints everything useful when a bug report comes in.
func runDoctor(
	svc interface {
		ConfigPath() string
		ListServers(string) ([]domain.Server, error)
		ListSSHKeys() ([]domain.SSHKey, error)
	},
	configPath, metaPath string,
	l interface{ Infof(string, ...any) },
) error {
	fmt.Printf("lazyssh      %s (%s)\n", version, commit)
	fmt.Printf("config       %s\n", configPath)
	fmt.Printf("metadata     %s\n", metaPath)
	if info, err := os.Stat(configPath); err == nil {
		fmt.Printf("config mode  %s (%d bytes)\n", info.Mode().Perm(), info.Size())
	} else {
		fmt.Printf("config mode  missing (%v)\n", err)
	}

	servers, err := svc.ListServers("")
	if err != nil {
		fmt.Printf("servers      error: %v\n", err)
	} else {
		fmt.Printf("servers      %d\n", len(servers))
	}

	keys, err := svc.ListSSHKeys()
	if err != nil {
		fmt.Printf("keys         error: %v\n", err)
	} else {
		loaded := 0
		for _, k := range keys {
			if k.LoadedInAgent {
				loaded++
			}
		}
		fmt.Printf("keys         %d (%d in agent)\n", len(keys), loaded)
	}

	fmt.Printf("ssh          %s\n", firstLine(commandOutput("ssh", "-V")))
	fmt.Printf("known_hosts  %s\n", existingPath(filepath.Join(filepath.Dir(configPath), "known_hosts")))
	l.Infof("doctor run")
	return nil
}

// commandOutput runs a command and returns its combined output.
func commandOutput(name string, args ...string) string {
	// #nosec G204 -- fixed command names, no user input
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return err.Error()
	}
	return string(out)
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "-"
}

// existingPath describes whether path exists.
func existingPath(path string) string {
	if info, err := os.Stat(path); err == nil {
		return fmt.Sprintf("%s (%d bytes)", path, info.Size())
	}
	return path + " (missing)"
}
