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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linpr/lazyssh/internal/core/domain"
	"go.uber.org/zap"
)

func newTestService(t *testing.T) *serverService {
	t.Helper()
	return &serverService{
		logger:           zap.NewNop().Sugar(),
		forwards:         make(map[string]*forwardProc),
		pingCache:        make(map[string]pingResult),
		pingSem:          make(chan struct{}, pingConcurrency),
		serverRepository: nil,
	}
}

// TestAskpassScriptDoesNotInterpolateSecret is the regression test for command
// injection through the password. The script must read the secret from the
// environment; feeding the previous implementation a password like a$(id)b
// executed `id` on save.
func TestAskpassScriptDoesNotInterpolateSecret(t *testing.T) {
	const payload = `a$(id)b"; rm -rf /tmp/should-not-exist; echo "`

	script := askpassScript()
	if strings.Contains(script, payload) {
		t.Fatal("askpass script contains the raw secret")
	}
	if !strings.Contains(script, "LAZYSSH_ASKPASS_SECRET") {
		t.Fatalf("askpass script should read the env var, got %q", script)
	}

	// The script must still print the secret verbatim, including shell metas.
	dir := t.TempDir()
	path := filepath.Join(dir, "askpass.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), "LAZYSSH_ASKPASS_SECRET="+payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if got := strings.TrimRight(string(out), "\n"); got != payload {
		t.Fatalf("script printed %q, want %q", got, payload)
	}
}

// TestForwardRuleFlags checks the ssh argument construction for every kind.
func TestForwardRuleFlags(t *testing.T) {
	tests := []struct {
		rule domain.ForwardRule
		want []string
	}{
		{domain.ForwardRule{Kind: domain.ForwardLocal, Spec: "8080 localhost:80"}, []string{"-L", "8080 localhost:80"}},
		{domain.ForwardRule{Kind: domain.ForwardRemote, Spec: "9000 localhost:22"}, []string{"-R", "9000 localhost:22"}},
		{domain.ForwardRule{Kind: domain.ForwardDynamic, Spec: "1080"}, []string{"-D", "1080"}},
	}
	for _, tt := range tests {
		got := tt.rule.Flag()
		if len(got) != len(tt.want) {
			t.Fatalf("%v: got %v, want %v", tt.rule.Kind, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("%v: got %v, want %v", tt.rule.Kind, got, tt.want)
			}
		}
	}
}

// TestControlPathIsScopedAndHashed proves the ControlMaster socket cannot escape
// its directory or collide with another alias.
func TestControlPathIsScopedAndHashed(t *testing.T) {
	original := os.Getenv("XDG_RUNTIME_DIR")
	t.Cleanup(func() { _ = os.Setenv("XDG_RUNTIME_DIR", original) })
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	nasty := "../../etc/passwd$(id)"
	p1 := controlPath(nasty)
	p2 := controlPath("other-host")

	if strings.Contains(p1, "..") || strings.Contains(p1, "/etc/") || strings.Contains(p1, "$") {
		t.Fatalf("control path escaped its scope: %q", p1)
	}
	if filepath.Dir(p1) != filepath.Dir(p2) {
		t.Fatalf("control paths should share a directory: %q vs %q", p1, p2)
	}
	if p1 == p2 {
		t.Fatal("distinct aliases must not share a control socket")
	}
	if base := filepath.Base(p1); len(base) > 40 {
		t.Fatalf("socket name %q is too long for sun_path", base)
	}
}

// TestValidateServer covers the service-level input validation.
func TestValidateServer(t *testing.T) {
	valid := domain.Server{Alias: "web1", Host: "10.0.0.1", Port: 22}
	if err := validateServer(valid); err != nil {
		t.Fatalf("valid server rejected: %v", err)
	}

	bad := []domain.Server{
		{Alias: "", Host: "10.0.0.1"},
		{Alias: "bad alias", Host: "10.0.0.1"},
		{Alias: "web1", Host: ""},
		{Alias: "web1", Host: "10.0.0.1", Port: 70000},
		{Alias: "web1", Host: "10.0.0.1", LocalForward: []string{"8080"}},
	}
	for _, srv := range bad {
		if err := validateServer(srv); err == nil {
			t.Errorf("expected error for %+v", srv)
		}
	}
}

// TestPingCachesResult makes sure repeated pings do not open a socket each time.
func TestPingCachesResult(t *testing.T) {
	svc := newTestService(t)
	srv := domain.Server{Alias: "cached", Host: "127.0.0.1", Port: 1}
	// Prime the cache manually with a distinguishable error and timestamp.
	svc.pingCache["cached"] = pingResult{up: true, latency: time.Second, err: nil, at: time.Now()}

	up, dur, err := svc.Ping(srv)
	if !up || err != nil || dur != time.Second {
		t.Fatalf("ping did not use the cache: up=%v dur=%v err=%v", up, dur, err)
	}

	// A stale entry must be ignored.
	svc.pingCache["cached"] = pingResult{up: true, latency: time.Second, at: time.Now().Add(-2 * pingCacheTTL)}
	up, _, _ = svc.Ping(srv)
	if up {
		t.Fatal("stale cache entry should not be reused (port 1 is closed)")
	}
}

// TestForwardLifecycle starts a harmless detached process through the public API.
func TestForwardLifecycle(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}
	svc := newTestService(t)
	// Port 1 on localhost is closed; ssh -N will exit, which is fine: the
	// lifecycle (register → status → stop) is what matters here.
	pid, err := svc.StartForward("127.0.0.1", []domain.ForwardRule{
		{Kind: domain.ForwardLocal, Spec: "1:127.0.0.1:1"},
	}, []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=1"})
	if err != nil {
		t.Fatalf("StartForward: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("pid = %d", pid)
	}
	statuses := svc.ForwardStatuses()
	if len(statuses) != 1 || statuses[0].Alias != "127.0.0.1" {
		t.Fatalf("unexpected statuses: %+v", statuses)
	}
	if err := svc.StopForwarding("127.0.0.1"); err != nil {
		t.Fatalf("StopForwarding: %v", err)
	}
	if svc.IsForwarding("127.0.0.1") {
		t.Fatal("forward still tracked after stop")
	}
}

// TestListSSHKeysIgnoresNonKeys is the regression test for known_hosts/config
// showing up in the key manager: they are 0600 like real keys, so the filter has
// to look at the file content.
func TestListSSHKeysIgnoresNonKeys(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("HOME", home)

	files := map[string]string{
		"config":          "Host web\n    HostName 10.0.0.1\n",
		"known_hosts":     "|1|abc=|def= ecdsa-sha2-nistp256 AAAAE2Vj\n",
		"kh":              "127.0.0.1 ecdsa-sha2-nistp256 AAAAE2Vj\n",
		"authorized_keys": "ssh-ed25519 AAAAC3Nza me@host\n",
		"id_ed25519":      "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(sshDir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	svc := newTestService(t)
	keys, err := svc.ListSSHKeys()
	if err != nil {
		t.Fatalf("ListSSHKeys: %v", err)
	}
	if len(keys) != 1 {
		var names []string
		for _, k := range keys {
			names = append(names, filepath.Base(k.Path))
		}
		t.Fatalf("expected only id_ed25519, got %v", names)
	}
	if base := filepath.Base(keys[0].Path); base != "id_ed25519" {
		t.Fatalf("got %q, want id_ed25519", base)
	}
}

// TestControlDirFallsBackWhenUnwritable is the regression test for every key-based
// connection silently falling back to the password prompt: when XDG_RUNTIME_DIR
// is not writable, controlDir() must return a directory that really works.
func TestControlDirFallsBackWhenUnwritable(t *testing.T) {
	// A path under a read-only parent cannot be created.
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.MkdirAll(ro, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", ro)

	dir := controlDir()
	if dir == "" {
		t.Fatal("controlDir returned an empty path")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("controlDir returned a non-existent directory %q: %v", dir, err)
	}
	// It must actually accept files, which is what ssh needs.
	f, err := os.CreateTemp(dir, "probe-*")
	if err != nil {
		t.Fatalf("directory %q is not writable: %v", dir, err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
}
