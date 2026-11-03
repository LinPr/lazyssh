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

package services

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/linpr/lazyssh/internal/core/domain"
)

// skipKeyFiles lists files in ~/.ssh that are not private keys.
var skipKeyFiles = map[string]bool{
	"known_hosts":      true,
	"known_hosts.old":  true,
	"authorized_keys":  true,
	"authorized_keys2": true,
	"config":           true,
	"config.bak":       true,
	"environment":      true,
}

// sshDir returns ~/.ssh.
func sshDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh"), nil
}

// ListSSHKeys enumerates private keys under ~/.ssh, enriched with fingerprint
// and type information from ssh-keygen. Keys that are passphrase protected are
// detected by asking ssh-keygen to read them without a passphrase.
func (s *serverService) ListSSHKeys() ([]domain.SSHKey, error) {
	dir, err := sshDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	agentLoaded := make(map[string]bool)
	if keys, err := s.AgentKeys(); err == nil {
		for _, k := range keys {
			agentLoaded[k.Path] = true
		}
	}

	var keys []domain.SSHKey
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, ".pub") || skipKeyFiles[name] {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Check the content, not just the mode: known_hosts, config and
		// authorized_keys are 0600 too, and the previous mode-only test listed
		// every one of them as a "key" (with a nonsense type and 0 bits).
		if !looksLikePrivateKey(path) {
			continue
		}

		key := domain.SSHKey{
			Path:          path,
			ModTime:       info.ModTime(),
			HasPublic:     fileExists(path + ".pub"),
			LoadedInAgent: agentLoaded[path],
		}
		enrichKey(&key)
		keys = append(keys, key)
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i].Path < keys[j].Path })
	return keys, nil
}

// AgentKeys lists the keys currently loaded in ssh-agent.
func (s *serverService) AgentKeys() ([]domain.SSHKey, error) {
	out, err := exec.Command("ssh-add", "-l").Output()
	if err != nil {
		// "Could not open a connection to your authentication agent" is the
		// normal case when no agent is running; not an error for the caller.
		return nil, nil
	}
	var keys []domain.SSHKey
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// 256 SHA256:abc… comment (ED25519)
		fields := strings.Fields(line)
		key := domain.SSHKey{LoadedInAgent: true}
		if len(fields) >= 2 {
			key.Fingerprint = fields[1]
		}
		if len(fields) >= 1 {
			if bits, err := strconv.Atoi(fields[0]); err == nil {
				key.Bits = bits
			}
		}
		if len(fields) >= 4 {
			key.Comment = strings.Join(fields[3:], " ")
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// AddKeyToAgent runs ssh-add for the given key.
func (s *serverService) AddKeyToAgent(path string) error {
	// #nosec G204
	cmd := exec.Command("ssh-add", path)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// GenerateKey creates a new key pair with ssh-keygen.
func (s *serverService) GenerateKey(opts domain.KeyGenOptions) (domain.SSHKey, error) {
	if opts.Path == "" {
		return domain.SSHKey{}, fmt.Errorf("key path is required")
	}
	path := expandTilde(opts.Path)
	if _, err := os.Stat(path); err == nil && !opts.Overwrite {
		return domain.SSHKey{}, fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return domain.SSHKey{}, err
	}

	keyType := opts.Type
	if keyType == "" {
		keyType = "ed25519"
	}
	bits := opts.Bits
	if bits == 0 {
		switch keyType {
		case "rsa":
			bits = 4096
		case "ecdsa":
			bits = 521
		}
	}

	args := []string{"-t", keyType, "-f", path, "-N", opts.Passphrase}
	if bits > 0 {
		args = append(args, "-b", strconv.Itoa(bits))
	}
	if opts.Comment != "" {
		args = append(args, "-C", opts.Comment)
	}

	// #nosec G204
	cmd := exec.Command("ssh-keygen", args...)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return domain.SSHKey{}, fmt.Errorf("ssh-keygen failed: %s", strings.TrimSpace(string(out)))
	}
	_ = os.Chmod(path, 0o600)

	key := domain.SSHKey{Path: path, Type: keyType, Bits: bits, Comment: opts.Comment}
	if info, err := os.Stat(path); err == nil {
		key.ModTime = info.ModTime()
	}
	key.HasPublic = fileExists(path + ".pub")
	enrichKey(&key)
	return key, nil
}

// CopyKeyToHost installs a public key on a host with ssh-copy-id.
func (s *serverService) CopyKeyToHost(path, alias string) error {
	// #nosec G204
	cmd := exec.Command("ssh-copy-id", "-i", path, alias)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-copy-id failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ListKnownHosts parses ~/.ssh/known_hosts (hashed entries are marked, not
// resolved).
func (s *serverService) ListKnownHosts() ([]domain.KnownHostEntry, error) {
	dir, err := sshDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "known_hosts")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []domain.KnownHostEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		entry := domain.KnownHostEntry{
			Line:     lineNo,
			Hosts:    fields[0],
			KeyType:  fields[1],
			Hashed:   strings.HasPrefix(fields[0], "|1|"),
			Modified: info.ModTime(),
		}
		if len(fields) >= 4 {
			entry.Comment = strings.Join(fields[3:], " ")
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// RemoveKnownHost deletes every known_hosts line matching host and returns how
// many lines were removed. Matching covers plain, comma-separated and hashed
// entries (via ssh-keygen -R).
func (s *serverService) RemoveKnownHost(host string) (int, error) {
	dir, err := sshDir()
	if err != nil {
		return 0, err
	}
	path := filepath.Join(dir, "known_hosts")
	if _, err := os.Stat(path); err != nil {
		return 0, nil
	}

	before, err := s.ListKnownHosts()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range before {
		if knownHostMatches(e.Hosts, host) {
			count++
		}
	}

	// ssh-keygen -R knows how to rewrite hashed lines and keeps a .old backup.
	// #nosec G204
	cmd := exec.Command("ssh-keygen", "-R", host)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return count, fmt.Errorf("ssh-keygen -R failed: %s", strings.TrimSpace(string(out)))
	}
	return count, nil
}

// knownHostMatches reports whether a known_hosts host field covers host.
func knownHostMatches(field, host string) bool {
	if strings.HasPrefix(field, "|1|") {
		return false // hashed: only ssh-keygen can answer this
	}
	for _, candidate := range strings.Split(field, ",") {
		if strings.EqualFold(candidate, host) {
			return true
		}
	}
	return false
}

// enrichKey fills type, bits, fingerprint and protection state from ssh-keygen.
func enrichKey(key *domain.SSHKey) {
	// #nosec G204
	if out, err := exec.Command("ssh-keygen", "-lf", key.Path).Output(); err == nil {
		fields := strings.Fields(string(out))
		// "256 SHA256:… comment (ED25519)"
		if len(fields) >= 2 {
			if bits, err := strconv.Atoi(fields[0]); err == nil {
				key.Bits = bits
			}
			key.Fingerprint = fields[1]
		}
		if len(fields) >= 3 && key.Comment == "" && !strings.HasPrefix(fields[2], "(") {
			key.Comment = fields[2]
		}
		if len(fields) >= 4 {
			key.Type = strings.ToLower(strings.Trim(fields[len(fields)-1], "()"))
		}
	}

	// A non-empty passphrase fails a dumb read with "incorrect passphrase" /
	// "load failed"; -y without a passphrase succeeds.
	cmd := exec.Command("ssh-keygen", "-y", "-P", "", "-f", key.Path)
	if err := cmd.Run(); err != nil {
		key.Protected = true
	}
}

// looksLikePrivateKey does a cheap header sniff for PEM/OpenSSH private keys.
func looksLikePrivateKey(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	return strings.Contains(head, "PRIVATE KEY")
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// expandTilde resolves a leading ~ in a path.
func expandTilde(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
		}
	}
	return path
}

// ageString renders a coarse "3d ago" style relative time for the UI.
func ageString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
