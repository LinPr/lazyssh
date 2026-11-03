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

package ssh_config_file

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"
)

// loadConfig reads and parses the SSH config file.
// If the file does not exist, it returns an empty config without error to support first-run behavior.
func (r *Repository) loadConfig() (*ssh_config.Config, error) {
	file, err := r.fileSystem.Open(r.configPath)
	if err != nil {
		if r.fileSystem.IsNotExist(err) {
			return &ssh_config.Config{Hosts: []*ssh_config.Host{}}, nil
		}
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			r.logger.Warnf("failed to close config file: %v", cerr)
		}
	}()

	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg, err := ssh_config.DecodeBytes(raw)
	if err != nil {
		// Upstream ssh_config (≥1.5) rejects Match criteria it does not implement
		// (Match user/localuser/originalhost/canonical/final and Match exec), while
		// the previous fork silently flattened those blocks into the enclosing Host.
		// Parse a sanitized copy so the list stays usable, but remember that the
		// file cannot be round-tripped faithfully and refuse to write it back.
		sanitized, changed := sanitizeUnsupportedMatch(raw)
		if !changed {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
		cfg, decodeErr := ssh_config.DecodeBytes(sanitized)
		if decodeErr != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
		r.readOnly.Store(true)
		r.logger.Warnw("ssh config contains Match directives the parser cannot represent; "+
			"lazyssh is running in read-only mode to avoid corrupting the file",
			"path", r.configPath,
			"match_error", err,
		)
		return cfg, nil
	}

	return cfg, nil
}

// sanitizeUnsupportedMatch rewrites Match directives that upstream ssh_config
// refuses to parse into an inert `Host !*` line, keeping the original text in a
// trailing comment so the block remains visible in the UI. Supported criteria
// (Match host / Match all) are left untouched. The second return value reports
// whether anything was rewritten.
func sanitizeUnsupportedMatch(raw []byte) ([]byte, bool) {
	lines := strings.Split(string(raw), "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Match") {
			continue
		}
		switch strings.ToLower(fields[1]) {
		case "host", "all":
			// Parsed natively by upstream.
		default:
			lines[i] = "Host !*  # lazyssh: unsupported Match directive kept as-is: " + trimmed
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	return []byte(strings.Join(lines, "\n")), true
}

// saveConfig writes the SSH config back to the file with atomic operations and backup management.
func (r *Repository) saveConfig(cfg *ssh_config.Config) error {
	if r.readOnly.Load() {
		return fmt.Errorf("ssh config left untouched: it contains Match directives that cannot be " +
			"round-tripped, edit the file manually")
	}

	configDir := filepath.Dir(r.configPath)

	tempFile, err := r.createTempFile(configDir)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}

	defer func() {
		if removeErr := r.fileSystem.Remove(tempFile); removeErr != nil {
			r.logger.Warnf("failed to remove temporary file %s: %v", tempFile, removeErr)
		}
	}()

	if err := r.writeConfigToFile(tempFile, cfg); err != nil {
		return fmt.Errorf("failed to write config to temporary file: %w", err)
	}

	// Ensure a one-time original backup exists before any modifications managed by lazyssh.
	if err := r.createOriginalBackupIfNeeded(); err != nil {
		return fmt.Errorf("failed to create original backup: %w", err)
	}

	if err := r.createBackup(); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	if err := r.fileSystem.Rename(tempFile, r.configPath); err != nil {
		return fmt.Errorf("failed to atomically replace config file: %w", err)
	}

	r.logger.Infof("SSH config successfully updated: %s", r.configPath)
	return nil
}

// writeConfigToFile writes the SSH config content to the specified file
func (r *Repository) writeConfigToFile(filePath string, cfg *ssh_config.Config) error {
	file, err := r.fileSystem.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, SSHConfigPerms)
	if err != nil {
		return fmt.Errorf("failed to open file for writing: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			r.logger.Warnf("failed to close file %s: %v", filePath, cerr)
		}
	}()

	configContent := cfg.String()
	if _, err := file.WriteString(configContent); err != nil {
		return fmt.Errorf("failed to write config content: %w", err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync file to disk: %w", err)
	}

	return nil
}

// createTempFile creates a temporary file in the specified directory
func (r *Repository) createTempFile(dir string) (string, error) {
	// os.CreateTemp gives a collision-free name and 0600 permissions; the previous
	// second-resolution timestamp could silently reuse the same temp file when two
	// writes happened within one second.
	f, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if cerr := f.Close(); cerr != nil {
		r.logger.Warnf("failed to close temporary file %s: %v", name, cerr)
	}

	return name, nil
}

// GetRawConfigBlock reads the config file and returns the raw text block for the
// given alias, including the "Host <alias>" line and everything up to the next
// Host/Match header.
//
// Included files are searched too: a host declared in ~/.ssh/config.d/* has no
// block in the main file, and the previous implementation reported "(config
// block not found)" for it.
func (r *Repository) GetRawConfigBlock(alias string) (string, error) {
	block, err := r.rawBlockIn(r.configPath, alias)
	if err != nil || block != "" {
		return block, err
	}
	// Fall back to Include targets (depth-limited to match expandIncludes).
	for _, path := range r.includeFiles(0) {
		block, err := r.rawBlockIn(path, alias)
		if err != nil {
			r.logger.Warnw("failed to read included config", "path", path, "error", err)
			continue
		}
		if block != "" {
			return block, nil
		}
	}
	return "", nil
}

// includeFiles lists every file reachable through Include directives.
func (r *Repository) includeFiles(depth int) []string {
	if depth > maxIncludeDepth {
		return nil
	}
	cfg, err := r.loadConfig()
	if err != nil {
		return nil
	}
	var files []string
	seen := make(map[string]bool)
	for _, host := range cfg.Hosts {
		for _, node := range host.Nodes {
			inc, ok := node.(*ssh_config.Include)
			if !ok {
				continue
			}
			for _, pattern := range includePatterns(inc.String()) {
				for _, path := range r.globInclude(pattern) {
					if seen[path] {
						continue
					}
					seen[path] = true
					files = append(files, path)
				}
			}
		}
	}
	return files
}

// rawBlockIn scans one file for the Host block belonging to alias.
func (r *Repository) rawBlockIn(path, alias string) (string, error) {
	file, err := r.fileSystem.Open(path)
	if err != nil {
		if r.fileSystem.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to open config file: %w", err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			r.logger.Warnf("failed to close config file: %v", cerr)
		}
	}()

	scanner := bufio.NewScanner(file)
	var lines []string
	inBlock := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "Host ") || strings.HasPrefix(trimmed, "Match ") {
			if inBlock {
				break
			}
			parts := strings.Fields(trimmed)
			if strings.HasPrefix(trimmed, "Host ") && len(parts) >= 2 {
				for _, candidate := range parts[1:] {
					if candidate == alias {
						inBlock = true
						lines = append(lines, line)
						break
					}
				}
			}
			continue
		}

		if inBlock {
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read config file: %w", err)
	}

	// Trim trailing blank lines so the details pane does not show dead space.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n"), nil
}
