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
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/linpr/lazyssh/internal/core/ports"
	"go.uber.org/zap"
)

type serverService struct {
	serverRepository ports.ServerRepository
	logger           *zap.SugaredLogger

	fwMu     sync.Mutex
	forwards map[string]*forwardProc

	// pingCache memoises recent TCP probes so a refresh with 100 hosts does not
	// open 100 sockets every time the list reloads.
	pingMu    sync.Mutex
	pingCache map[string]pingResult

	// pingSem bounds how many probes run at once.
	pingSem chan struct{}

	// configPath is the ssh config file this service operates on.
	configPath string
}

// pingResult is one cached reachability probe.
type pingResult struct {
	up      bool
	latency time.Duration
	err     error
	at      time.Time
}

const (
	pingCacheTTL      = 20 * time.Second
	pingConcurrency   = 8
	sshDialTimeout    = 3 * time.Second
	controlPersistSec = "600"
)

// NewServerService creates a new instance of serverService.
func NewServerService(logger *zap.SugaredLogger, sr ports.ServerRepository, configPath string) ports.ServerService {
	return &serverService{
		logger:           logger,
		serverRepository: sr,
		forwards:         make(map[string]*forwardProc),
		pingCache:        make(map[string]pingResult),
		pingSem:          make(chan struct{}, pingConcurrency),
		configPath:       configPath,
	}
}

// ConfigPath returns the ssh config file the service operates on.
func (s *serverService) ConfigPath() string { return s.configPath }

// ListServers returns a list of servers sorted with pinned on top.
func (s *serverService) ListServers(query string) ([]domain.Server, error) {
	servers, err := s.serverRepository.ListServers(query)
	if err != nil {
		s.logger.Errorw("failed to list servers", "error", err)
		return nil, err
	}

	// Sort: pinned first (PinnedAt non-zero), then by PinnedAt desc, then by Alias asc.
	sort.SliceStable(servers, func(i, j int) bool {
		pi := !servers[i].PinnedAt.IsZero()
		pj := !servers[j].PinnedAt.IsZero()
		if pi != pj {
			return pi
		}
		if pi && pj {
			return servers[i].PinnedAt.After(servers[j].PinnedAt)
		}
		return servers[i].Alias < servers[j].Alias
	})

	return servers, nil
}

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// validateServer performs core validation of server fields.
func validateServer(srv domain.Server) error {
	if strings.TrimSpace(srv.Alias) == "" {
		return fmt.Errorf("alias is required")
	}
	if !aliasPattern.MatchString(srv.Alias) {
		return fmt.Errorf("alias may contain letters, digits, dot, dash, underscore")
	}
	if strings.TrimSpace(srv.Host) == "" {
		return fmt.Errorf("Host/IP is required")
	}
	if ip := net.ParseIP(srv.Host); ip == nil {
		if strings.Contains(srv.Host, " ") {
			return fmt.Errorf("host must not contain spaces")
		}
		if !hostPattern.MatchString(srv.Host) {
			return fmt.Errorf("host contains invalid characters")
		}
		if strings.HasPrefix(srv.Host, ".") || strings.HasSuffix(srv.Host, ".") {
			return fmt.Errorf("host must not start or end with a dot")
		}
		for _, lbl := range strings.Split(srv.Host, ".") {
			if lbl == "" {
				return fmt.Errorf("host must not contain empty labels")
			}
			if strings.HasPrefix(lbl, "-") || strings.HasSuffix(lbl, "-") {
				return fmt.Errorf("hostname labels must not start or end with a hyphen")
			}
		}
	}
	if srv.Port != 0 && (srv.Port < 1 || srv.Port > 65535) {
		return fmt.Errorf("port must be a number between 1 and 65535")
	}
	for _, rule := range forwardRulesOf(srv) {
		if err := validateForwardSpec(rule); err != nil {
			return err
		}
	}
	return nil
}

// forwardRulesOf collects every forwarding rule declared on a server.
func forwardRulesOf(srv domain.Server) []domain.ForwardRule {
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

// validateForwardSpec performs a light structural check on a forward spec.
func validateForwardSpec(rule domain.ForwardRule) error {
	spec := strings.TrimSpace(rule.Spec)
	if spec == "" {
		return nil
	}
	switch rule.Kind {
	case domain.ForwardDynamic:
		// host:port or port
		if _, err := parseForwardPort(spec); err != nil {
			return fmt.Errorf("invalid DynamicForward %q: %w", spec, err)
		}
	default:
		// [bind:]port:host:hostport
		if strings.Count(spec, ":") < 2 {
			return fmt.Errorf("invalid %s forward %q: expected [bind:]port:host:hostport", rule.Kind, spec)
		}
	}
	return nil
}

// parseForwardPort parses the trailing port of a spec like "1080" or "127.0.0.1:1080".
func parseForwardPort(spec string) (int, error) {
	idx := strings.LastIndex(spec, ":")
	portStr := spec
	if idx >= 0 {
		portStr = spec[idx+1:]
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("port must be 1-65535")
	}
	return p, nil
}

// UpdateServer updates an existing server with new details.
func (s *serverService) UpdateServer(server domain.Server, newServer domain.Server) error {
	if err := validateServer(newServer); err != nil {
		s.logger.Warnw("validation failed on update", "error", err, "server", newServer)
		return err
	}
	err := s.serverRepository.UpdateServer(server, newServer)
	if err != nil {
		s.logger.Errorw("failed to update server", "error", err, "server", server)
	}
	return err
}

// AddServer adds a new server to the repository.
func (s *serverService) AddServer(server domain.Server) error {
	if err := validateServer(server); err != nil {
		s.logger.Warnw("validation failed on add", "error", err, "server", server)
		return err
	}
	err := s.serverRepository.AddServer(server)
	if err != nil {
		s.logger.Errorw("failed to add server", "error", err, "server", server)
	}
	return err
}

// DeleteServer removes a server from the repository.
func (s *serverService) DeleteServer(server domain.Server) error {
	err := s.serverRepository.DeleteServer(server)
	if err != nil {
		s.logger.Errorw("failed to delete server", "error", err, "server", server)
	}
	return err
}

// SetPinned sets or clears a pin timestamp for the server alias.
func (s *serverService) SetPinned(alias string, pinned bool) error {
	err := s.serverRepository.SetPinned(alias, pinned)
	if err != nil {
		s.logger.Errorw("failed to set pin state", "error", err, "alias", alias, "pinned", pinned)
	}
	return err
}

// RecordConnect bumps the connect counter and last-seen timestamp for alias.
func (s *serverService) RecordConnect(alias string) error {
	err := s.serverRepository.RecordSSH(alias)
	if err != nil {
		s.logger.Errorw("failed to record ssh metadata", "alias", alias, "error", err)
	}
	return err
}

// SSH starts an interactive SSH session to the given alias using the system's ssh client.
func (s *serverService) SSH(alias string) error {
	s.logger.Infow("ssh start", "alias", alias)
	cmd := exec.Command("ssh", alias)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		s.logger.Errorw("ssh command failed", "alias", alias, "error", err)
		return err
	}
	if err := s.RecordConnect(alias); err != nil {
		return err
	}
	s.logger.Infow("ssh end", "alias", alias)
	return nil
}

// SSHWithArgs runs system ssh with provided extra args (e.g., -L/-R/-D) for the given alias.
func (s *serverService) SSHWithArgs(alias string, extraArgs []string) error {
	s.logger.Infow("ssh start (with args)", "alias", alias, "args", extraArgs)
	args := append([]string{}, extraArgs...)
	args = append(args, alias)
	// #nosec G204
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		s.logger.Errorw("ssh (with args) failed", "alias", alias, "error", err)
		return err
	}
	if err := s.RecordConnect(alias); err != nil {
		return err
	}
	s.logger.Infow("ssh end (with args)", "alias", alias)
	return nil
}

// Ping checks if the server is reachable on its SSH port, with a short-lived
// cache and a bounded number of concurrent probes.
func (s *serverService) Ping(server domain.Server) (bool, time.Duration, error) {
	key := server.Alias
	if key == "" {
		key = net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
	}

	s.pingMu.Lock()
	cached, ok := s.pingCache[key]
	s.pingMu.Unlock()
	if ok && time.Since(cached.at) < pingCacheTTL {
		return cached.up, cached.latency, cached.err
	}

	if s.pingSem != nil {
		s.pingSem <- struct{}{}
		defer func() { <-s.pingSem }()
	}

	start := time.Now()
	host, port, err := s.ResolveDestination(server.Alias)
	if err != nil || host == "" {
		host = strings.TrimSpace(server.Host)
		if host == "" {
			host = server.Alias
		}
		port = server.Port
		if port == 0 {
			port = 22
		}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	dialer := net.Dialer{Timeout: sshDialTimeout}
	conn, dialErr := dialer.Dial("tcp", addr)
	latency := time.Since(start)
	up := dialErr == nil
	if conn != nil {
		_ = conn.Close()
	}

	s.pingMu.Lock()
	s.pingCache[key] = pingResult{up: up, latency: latency, err: dialErr, at: time.Now()}
	s.pingMu.Unlock()

	return up, latency, dialErr
}

// ResolveDestination resolves alias to the host and port OpenSSH would use.
func (s *serverService) ResolveDestination(alias string) (string, int, error) {
	return resolveSSHDestination(alias)
}

// GetRawConfigBlock returns the raw SSH config text block for the given alias.
func (s *serverService) GetRawConfigBlock(alias string) (string, error) {
	return s.serverRepository.GetRawConfigBlock(alias)
}

// controlPath returns the ControlMaster socket path for the given alias.
//
// The socket lives under the user's runtime dir (0700, per-user) with a hashed
// name: a predictable /tmp/lazyssh-cm-<alias> could be pre-created by another
// local user to hijack the multiplexed connection.
func controlPath(alias string) string {
	return filepath.Join(controlDir(), controlSocketName(alias))
}

// controlSocketName hashes the alias so arbitrary characters and long names
// cannot escape the directory or exceed the sun_path limit.
func controlSocketName(alias string) string {
	sum := sha256.Sum256([]byte(alias))
	return fmt.Sprintf("cm-%x", sum[:10])
}

// controlDir returns (and creates) the directory holding our control sockets.
// controlDir returns a private, writable directory for ControlMaster sockets.
//
// The previous version ignored MkdirAll's error and handed back a path that did
// not exist. ssh then failed with "unix_listener: cannot bind to path … No such
// file or directory", the batch probe failed, and every connection fell through
// to the password prompt even with working key auth. A read-only or missing
// XDG_RUNTIME_DIR is common enough (containers, sudo, some CI) that this must
// degrade to the temp dir instead of failing.
func controlDir() string {
	name := fmt.Sprintf("lazyssh-%d", os.Getuid())

	candidates := make([]string, 0, 2)
	if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
		candidates = append(candidates, filepath.Join(base, name))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), name))

	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			continue
		}
		if probeWritable(dir) {
			return dir
		}
	}
	// Nothing writable: return the temp path anyway so ssh reports a clear error
	// rather than us silently pretending a directory exists.
	return candidates[len(candidates)-1]
}

// probeWritable checks that a directory really accepts files (MkdirAll succeeds
// on an existing directory even when the filesystem is read-only).
func probeWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// SSHMasterExists checks whether a ControlMaster socket for alias is alive.
func (s *serverService) SSHMasterExists(alias string) bool {
	cp := controlPath(alias)
	// #nosec G204
	cmd := exec.Command("ssh", "-O", "check", "-o", "ControlPath="+cp, alias)
	return cmd.Run() == nil
}

// EstablishSSHMaster starts a ControlMaster connection for alias (blocks until
// the connection is established or fails).
func (s *serverService) EstablishSSHMaster(alias string) error {
	if s.SSHMasterExists(alias) {
		return nil
	}
	cp := controlPath(alias)
	// #nosec G204
	cmd := exec.Command("ssh",
		"-o", "ControlMaster=yes",
		"-o", "ControlPath="+cp,
		"-o", "ControlPersist="+controlPersistSec,
		"-N",
		alias,
	)
	return cmd.Run()
}

// TrySSHMasterBatch attempts to establish a ControlMaster without any interactive
// prompts (BatchMode=yes).
func (s *serverService) TrySSHMasterBatch(alias string) error {
	cp := controlPath(alias)
	// #nosec G204
	cmd := exec.Command("ssh",
		"-o", "ControlMaster=yes",
		"-o", "ControlPath="+cp,
		"-o", "ControlPersist="+controlPersistSec,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-N", alias,
	)
	return cmd.Run()
}

// EstablishSSHMasterWithPassword establishes a ControlMaster using a temporary
// SSH_ASKPASS script.
//
// The secret is handed to the script through the environment, never interpolated
// into shell source: formatting it with %q produced double quotes that a POSIX
// shell still expands, so a password like a$(id)b executed a command.
func (s *serverService) EstablishSSHMasterWithPassword(alias, password string) error {
	if s.SSHMasterExists(alias) {
		return nil
	}
	cp := controlPath(alias)

	dir, err := os.MkdirTemp("", "lazyssh-askpass-*")
	if err != nil {
		return fmt.Errorf("failed to create askpass dir: %w", err)
	}
	defer os.RemoveAll(dir) //nolint:errcheck

	scriptPath := filepath.Join(dir, "askpass.sh")
	if err := os.WriteFile(scriptPath, []byte(askpassScript()), 0o700); err != nil {
		return fmt.Errorf("failed to write askpass script: %w", err)
	}

	// #nosec G204
	cmd := exec.Command("ssh",
		"-o", "ControlMaster=yes",
		"-o", "ControlPath="+cp,
		"-o", "ControlPersist="+controlPersistSec,
		"-o", "ConnectTimeout=10",
		"-N", alias,
	)
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+scriptPath,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
		"LAZYSSH_ASKPASS_SECRET="+password,
	)
	cmd.Stdin = nil

	sysProcAttr := &syscall.SysProcAttr{}
	setDetach(sysProcAttr)
	cmd.SysProcAttr = sysProcAttr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ssh master: %w", err)
	}
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.SSHMasterExists(alias) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for SSH master socket (%s)", cp)
}

// CloseSSHMaster sends an exit signal to the ControlMaster for alias.
func (s *serverService) CloseSSHMaster(alias string) error {
	cp := controlPath(alias)
	// #nosec G204
	cmd := exec.Command("ssh", "-O", "exit", "-o", "ControlPath="+cp, alias)
	return cmd.Run()
}

// ValidateConfig parses the ssh config and reports problems without touching it.
func (s *serverService) ValidateConfig() ([]string, error) {
	servers, err := s.serverRepository.ListServers("")
	if err != nil {
		return nil, err
	}
	var problems []string
	seen := make(map[string]int)
	for _, srv := range servers {
		seen[srv.Alias]++
		if srv.Host == "" {
			problems = append(problems, fmt.Sprintf("%s: missing HostName", srv.Alias))
		}
		if err := validateServer(srv); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", srv.Alias, err))
		}
		for _, rule := range forwardRulesOf(srv) {
			if err := validateForwardSpec(rule); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", srv.Alias, err))
			}
		}
	}
	for alias, n := range seen {
		if n > 1 {
			problems = append(problems, fmt.Sprintf("%s: defined %d times", alias, n))
		}
	}
	sort.Strings(problems)
	return problems, nil
}

// resolveSSHDestination uses `ssh -G <alias>` to extract HostName and Port.
func resolveSSHDestination(alias string) (string, int, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", 0, fmt.Errorf("empty alias")
	}
	// #nosec G204
	cmd := exec.Command("ssh", "-G", alias)
	out, err := cmd.Output()
	if err != nil {
		return "", 0, err
	}
	host := ""
	port := 0
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "hostname":
			host = fields[1]
		case "port":
			if p, err := strconv.Atoi(fields[1]); err == nil {
				port = p
			}
		}
	}
	if host == "" {
		host = alias
	}
	if port == 0 {
		port = 22
	}
	return host, port, nil
}

// askpassScript is the SSH_ASKPASS helper. It reads the secret from the
// environment, so a password containing shell metacharacters (a$(id)b, quotes,
// newlines) is never interpreted. The previous implementation interpolated it
// into the script with %q, which a POSIX shell still expands.
func askpassScript() string {
	return "#!/bin/sh\nprintf '%s\\n' \"$LAZYSSH_ASKPASS_SECRET\"\n"
}
