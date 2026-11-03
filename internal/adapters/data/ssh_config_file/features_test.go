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
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func writeFixture(t *testing.T, files map[string]string) (configPath string, home string) {
	t.Helper()
	home = t.TempDir()
	for rel, content := range files {
		full := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return filepath.Join(home, ".ssh", "config"), home
}

func newRepoFor(t *testing.T, configPath, home string) *Repository {
	t.Helper()
	// expandIncludes resolves "~" through os.UserHomeDir, which honours $HOME.
	t.Setenv("HOME", home)
	return &Repository{
		logger:          zap.NewNop().Sugar(),
		configPath:      configPath,
		fileSystem:      DefaultFileSystem{},
		metadataManager: newMetadataManager(filepath.Join(home, "meta.json"), zap.NewNop().Sugar()),
	}
}

// TestIncludeHostsAppearInList covers the common "Include ~/.ssh/config.d/*"
// layout: hosts declared in included files must be listed, not just the ones in
// the top-level file.
func TestIncludeHostsAppearInList(t *testing.T) {
	cfg, home := writeFixture(t, map[string]string{
		".ssh/config":     "Include ~/.ssh/config.d/*\n\nHost root-host\n    HostName 1.1.1.1\n    User root\n",
		".ssh/config.d/a": "Host jump1\n    HostName jump1.example.com\n    User qinyi.lin\n",
		".ssh/config.d/b": "Host jump3\n    HostName jump3.example.com\n",
	})
	repo := newRepoFor(t, cfg, home)

	servers, err := repo.ListServers("")
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	got := map[string]bool{}
	for _, s := range servers {
		got[s.Alias] = true
	}
	for _, want := range []string{"root-host", "jump1", "jump3"} {
		if !got[want] {
			t.Errorf("included host %q missing from list (got %v)", want, keysOf(got))
		}
	}
}

// TestIncludedHostRawBlock checks the details pane finds blocks in included files.
func TestIncludedHostRawBlock(t *testing.T) {
	cfg, home := writeFixture(t, map[string]string{
		".ssh/config":     "Include ~/.ssh/config.d/*\n\nHost root-host\n    HostName 1.1.1.1\n",
		".ssh/config.d/x": "Host jump1\n    HostName jump1.example.com\n    User bob\n",
	})
	repo := newRepoFor(t, cfg, home)

	block, err := repo.GetRawConfigBlock("jump1")
	if err != nil {
		t.Fatalf("GetRawConfigBlock: %v", err)
	}
	if block == "" {
		t.Fatal("expected the included host block, got empty")
	}
	if want := "Host jump1"; !contains(block, want) {
		t.Fatalf("block missing %q: %q", want, block)
	}
	if want := "User bob"; !contains(block, want) {
		t.Fatalf("block missing %q: %q", want, block)
	}
}

// TestEditKeepsUnexposedFields is the regression test for silent data loss: the
// edit form only exposes ~30 fields, everything else must survive a save.
func TestEditKeepsUnexposedFields(t *testing.T) {
	const src = "Host web1\n" +
		"    HostName 10.0.0.1\n" +
		"    User root\n" +
		"    RemoteCommand uptime\n" +
		"    RequestTTY force\n" +
		"    ForwardAgent yes\n" +
		"    SendEnv LANG\n"
	repo, cfgPath := newTestRepo(t, src)

	servers, err := repo.ListServers("")
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	before := servers[0]

	// Simulate the form: start from the on-disk server, only overwrite User, the
	// way FormModel.ToServer now does.
	after := before
	after.User = "admin"
	if err := repo.UpdateServer(before, after); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}

	got := readFile(t, cfgPath)
	for _, key := range []string{"RemoteCommand uptime", "RequestTTY force", "ForwardAgent yes", "SendEnv LANG"} {
		if !contains(got, key) {
			t.Errorf("%q was dropped by the edit path:\n%s", key, got)
		}
	}
	if !contains(got, "User admin") || contains(got, "User root") {
		t.Errorf("User was not updated:\n%s", got)
	}
}

// TestSearchTagAndTextQuery checks that one query language serves both the list
// and the repository (previously "#tag" only worked in the UI layer).
func TestSearchTagAndTextQuery(t *testing.T) {
	const src = "Host web-prod\n    HostName 10.0.0.1\n    User deploy\n\n" +
		"Host db-prod\n    HostName 10.0.0.2\n    User postgres\n\n" +
		"Host web-dev\n    HostName 10.0.0.3\n    User deploy\n"
	repo, _ := newTestRepo(t, src)
	if err := repo.metadataManager.saveAll(map[string]ServerMetadata{
		"web-prod": {Tags: []string{"prod", "web"}},
		"db-prod":  {Tags: []string{"prod", "db"}},
		"web-dev":  {Tags: []string{"dev", "web"}},
	}); err != nil {
		t.Fatalf("saveAll: %v", err)
	}

	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"web-prod", "db-prod", "web-dev"}},
		{"#prod", []string{"web-prod", "db-prod"}},
		{"#prod #web", []string{"web-prod"}},
		{"web #prod", []string{"web-prod"}},
		{"nothing-matches", nil},
	}
	for _, tt := range tests {
		servers, err := repo.ListServers(tt.query)
		if err != nil {
			t.Fatalf("ListServers(%q): %v", tt.query, err)
		}
		var got []string
		for _, s := range servers {
			got = append(got, s.Alias)
		}
		if !equalStrings(got, tt.want) {
			t.Errorf("query %q: got %v, want %v", tt.query, got, tt.want)
		}
	}
}

// TestAtomicMetadataWrite makes sure a failed/partial write cannot truncate the
// existing metadata file.
func TestAtomicMetadataWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	mm := newMetadataManager(path, zap.NewNop().Sugar())

	if err := mm.setPinned("web1", true); err != nil {
		t.Fatalf("setPinned: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("metadata file is empty after write")
	}

	// No leftover temp files should remain next to the target.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "metadata.json" {
			t.Errorf("unexpected leftover file %q", e.Name())
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
