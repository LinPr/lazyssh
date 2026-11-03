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
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/linpr/lazyssh/internal/core/domain"
)

// forwardProc is one detached `ssh -N` process together with the rules it serves.
type forwardProc struct {
	cmd       *exec.Cmd
	pid       int
	rules     []domain.ForwardRule
	startedAt time.Time
}

// ForwardRules derives the forwarding rules declared in the SSH config for alias.
func (s *serverService) ForwardRules(alias string) []domain.ForwardRule {
	servers, err := s.serverRepository.ListServers("")
	if err != nil {
		s.logger.Errorw("failed to load servers while reading forward rules", "alias", alias, "error", err)
		return nil
	}
	for _, srv := range servers {
		if srv.Alias != alias {
			continue
		}
		var rules []domain.ForwardRule
		for _, spec := range srv.LocalForward {
			rules = append(rules, domain.ForwardRule{Kind: domain.ForwardLocal, Spec: spec})
		}
		for _, spec := range srv.RemoteForward {
			rules = append(rules, domain.ForwardRule{Kind: domain.ForwardRemote, Spec: spec})
		}
		for _, spec := range srv.DynamicForward {
			rules = append(rules, domain.ForwardRule{Kind: domain.ForwardDynamic, Spec: spec})
		}
		return rules
	}
	return nil
}

// StartForward launches `ssh -N` with the given forwarding rules.
//
// Passing no rules falls back to whatever the host declares in ssh_config, which
// is what the UI's "F" key does. Previously this always received nil extra args,
// so it started a plain `ssh -N` that forwarded nothing at all.
func (s *serverService) StartForward(alias string, rules []domain.ForwardRule, extraArgs []string) (int, error) {
	if len(rules) == 0 {
		rules = s.ForwardRules(alias)
	}

	args := make([]string, 0, len(extraArgs)+len(rules)*2+2)
	args = append(args, extraArgs...)
	for _, rule := range rules {
		args = append(args, rule.Flag()...)
	}
	args = append(args, "-N", alias)

	// #nosec G204 -- alias comes from the user's own ssh config and is validated
	cmd := exec.Command("ssh", args...)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to open devnull: %w", err)
	}

	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	// Detach so the forwarding survives the TUI exiting.
	sysProcAttr := &syscall.SysProcAttr{}
	setDetach(sysProcAttr)
	cmd.SysProcAttr = sysProcAttr

	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		return 0, fmt.Errorf("failed to start ssh: %w", err)
	}

	proc := cmd.Process
	if proc == nil {
		_ = devNull.Close()
		return 0, fmt.Errorf("process is nil after start")
	}
	pid := proc.Pid

	entry := &forwardProc{cmd: cmd, pid: pid, rules: rules, startedAt: time.Now()}

	s.fwMu.Lock()
	if s.forwards == nil {
		s.forwards = make(map[string]*forwardProc)
	}
	s.forwards[alias] = entry
	s.fwMu.Unlock()

	// Reap the child and drop it from the map when it exits on its own.
	go func(a string, p int, c *exec.Cmd, dn *os.File) {
		_ = c.Wait()
		_ = dn.Close()

		s.fwMu.Lock()
		defer s.fwMu.Unlock()
		if cur, ok := s.forwards[a]; ok && cur.pid == p {
			delete(s.forwards, a)
		}
	}(alias, pid, cmd, devNull)

	return pid, nil
}

// StopForwarding terminates the forwarding process for alias.
func (s *serverService) StopForwarding(alias string) error {
	s.fwMu.Lock()
	entry := s.forwards[alias]
	delete(s.forwards, alias)
	s.fwMu.Unlock()

	if entry == nil || entry.cmd.Process == nil {
		return nil
	}

	proc := entry.cmd.Process
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if killErr := proc.Kill(); killErr != nil && !isProcessDone(killErr) {
			return fmt.Errorf("failed to stop forwarding for %s: %w", alias, killErr)
		}
	}

	// Give ssh a moment to release the listening ports before we report success.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(proc.Pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

// IsForwarding reports whether a forwarding process is currently tracked for alias.
func (s *serverService) IsForwarding(alias string) bool {
	s.fwMu.Lock()
	defer s.fwMu.Unlock()
	entry, ok := s.forwards[alias]
	if !ok {
		return false
	}
	return entry.cmd.Process != nil && processAlive(entry.pid)
}

// ForwardStatuses returns a snapshot of every tracked forward, newest first.
func (s *serverService) ForwardStatuses() []domain.ForwardStatus {
	s.fwMu.Lock()
	defer s.fwMu.Unlock()

	out := make([]domain.ForwardStatus, 0, len(s.forwards))
	for alias, entry := range s.forwards {
		out = append(out, domain.ForwardStatus{
			Alias:     alias,
			Rules:     entry.rules,
			PID:       entry.pid,
			StartedAt: entry.startedAt,
			Running:   entry.cmd.Process != nil && processAlive(entry.pid),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// processAlive reports whether pid is still running (signal 0 probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// isProcessDone reports whether err just means "already exited".
func isProcessDone(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "process already finished") || strings.Contains(msg, "no such process")
}
