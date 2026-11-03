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
	"strings"
	"testing"

	"github.com/kevinburke/ssh_config"
	"github.com/linpr/lazyssh/internal/core/domain"
	"go.uber.org/zap"
)

func newTestRepo(t *testing.T, contents string) (*Repository, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	if contents != "" {
		if err := os.WriteFile(cfgPath, []byte(contents), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	repo := &Repository{
		logger:          zap.NewNop().Sugar(),
		configPath:      cfgPath,
		fileSystem:      DefaultFileSystem{},
		metadataManager: newMetadataManager(filepath.Join(dir, "metadata.json"), zap.NewNop().Sugar()),
	}
	return repo, cfgPath
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestAddServerWritesIndentedBlock guards the migration away from the
// adembc fork: upstream ssh_config keeps KV indentation inside the key, so new
// blocks must still be indented with four spaces.
func TestAddServerWritesIndentedBlock(t *testing.T) {
	repo, cfgPath := newTestRepo(t, "")

	srv := domain.Server{
		Alias:         "web1",
		Host:          "10.0.0.1",
		User:          "root",
		Port:          22,
		IdentityFiles: []string{"~/.ssh/id_ed25519"},
	}
	if err := repo.AddServer(srv); err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	got := readFile(t, cfgPath)
	want := "Host web1 #Added by lazyssh\n" +
		"    HostName 10.0.0.1\n" +
		"    User root\n" +
		"    Port 22\n" +
		"    IdentityFile ~/.ssh/id_ed25519\n"
	if got != want {
		t.Fatalf("unexpected config:\n got: %q\nwant: %q", got, want)
	}
}

// TestUpdateServerRewritesParsedValues makes sure editing an existing host
// actually changes the file. Upstream ssh_config keeps the raw value text for
// round-tripping, so in-place KV.Value mutation would silently be ignored.
func TestUpdateServerRewritesParsedValues(t *testing.T) {
	repo, cfgPath := newTestRepo(t, "Host web1\n    HostName 10.0.0.1\n    User root\n")

	before := domain.Server{Alias: "web1", Host: "10.0.0.1", User: "root", Port: 22}
	after := domain.Server{Alias: "web1", Host: "10.0.0.2", User: "admin", Port: 2222}
	if err := repo.UpdateServer(before, after); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}

	got := readFile(t, cfgPath)
	if !strings.Contains(got, "HostName 10.0.0.2") || !strings.Contains(got, "User admin") {
		t.Fatalf("values were not rewritten: %q", got)
	}
	if strings.Contains(got, "User root") {
		t.Fatalf("stale value survived: %q", got)
	}
	if !strings.Contains(got, "Port 2222") {
		t.Fatalf("new Port missing: %q", got)
	}

	// Round-trip stability: re-parsing and re-serializing must not change bytes.
	cfg, err := ssh_config.DecodeBytes([]byte(got))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.String() != got {
		t.Fatalf("config is not round-trip stable:\n got: %q\nwant: %q", cfg.String(), got)
	}
}

// TestRemoveFieldFromParsedHost covers clearing a value (the update path deletes
// nodes whose new value is empty).
func TestRemoveFieldFromParsedHost(t *testing.T) {
	repo, cfgPath := newTestRepo(t, "Host web1\n    HostName 10.0.0.1\n    User root\n    Compression yes\n")

	before := domain.Server{Alias: "web1", Host: "10.0.0.1", User: "root", Compression: "yes"}
	after := domain.Server{Alias: "web1", Host: "10.0.0.1", User: "root"}
	if err := repo.UpdateServer(before, after); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}

	got := readFile(t, cfgPath)
	if strings.Contains(got, "Compression") {
		t.Fatalf("Compression should have been removed: %q", got)
	}
	if !strings.Contains(got, "HostName 10.0.0.1") {
		t.Fatalf("unrelated values were lost: %q", got)
	}
}

// TestRenameAliasRewritesPattern verifies pattern replacement uses NewPattern,
// since upstream Pattern fields are unexported.
func TestRenameAliasRewritesPattern(t *testing.T) {
	repo, cfgPath := newTestRepo(t, "Host web1\n    HostName 10.0.0.1\n")

	before := domain.Server{Alias: "web1", Host: "10.0.0.1", Port: 22}
	after := domain.Server{Alias: "web-prod", Host: "10.0.0.1", Port: 22}
	if err := repo.UpdateServer(before, after); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}

	got := readFile(t, cfgPath)
	if !strings.Contains(got, "Host web-prod") || strings.Contains(got, "Host web1") {
		t.Fatalf("alias was not renamed: %q", got)
	}

	servers, err := repo.ListServers("")
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 1 || servers[0].Alias != "web-prod" {
		t.Fatalf("unexpected servers: %+v", servers)
	}
}

// TestUnsupportedMatchDirectiveMakesRepoReadOnly documents the deliberate
// degradation: upstream cannot represent `Match user`, so the list is still
// readable but writes are refused instead of silently dropping the block.
func TestUnsupportedMatchDirectiveMakesRepoReadOnly(t *testing.T) {
	const src = "Host web1\n    HostName 10.0.0.1\n\nMatch user bob\n    Port 2222\n\nHost web2\n    HostName 10.0.0.2\n"
	repo, cfgPath := newTestRepo(t, src)

	servers, err := repo.ListServers("")
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) < 2 {
		t.Fatalf("expected both real hosts, got %+v", servers)
	}
	if !repo.readOnly.Load() {
		t.Fatal("repository should be marked read-only")
	}

	err = repo.AddServer(domain.Server{Alias: "web3", Host: "10.0.0.3", Port: 22})
	if err == nil || !strings.Contains(err.Error(), "cannot be round-tripped") {
		t.Fatalf("expected read-only error, got %v", err)
	}
	if got := readFile(t, cfgPath); got != src {
		t.Fatalf("config must be untouched:\n got: %q\nwant: %q", got, src)
	}
}

// TestMatchHostIsSupported confirms the supported Match form keeps the repo
// writable, since upstream parses it natively.
func TestMatchHostIsSupported(t *testing.T) {
	const src = "Host web1\n    HostName 10.0.0.1\n\nMatch host *.example.com\n    User bob\n"
	repo, _ := newTestRepo(t, src)

	if _, err := repo.ListServers(""); err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if repo.readOnly.Load() {
		t.Fatal("Match host is supported upstream; repo must stay writable")
	}
}

func TestSanitizeUnsupportedMatch(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantChanged bool
		want        string
	}{
		{
			name:        "supported criteria untouched",
			in:          "Host a\n  HostName 1.1.1.1\n\nMatch host *.example.com\n  User bob\n\nMatch all\n  Port 22\n",
			wantChanged: false,
		},
		{
			name:        "match user rewritten",
			in:          "Match user bob\n  Port 2222\n",
			wantChanged: true,
			want:        "Host !*  # lazyssh: unsupported Match directive kept as-is: Match user bob\n  Port 2222\n",
		},
		{
			name:        "match exec rewritten",
			in:          "Match exec \"test 1\"\n  Port 22\n",
			wantChanged: true,
			want:        "Host !*  # lazyssh: unsupported Match directive kept as-is: Match exec \"test 1\"\n  Port 22\n",
		},
		{
			name:        "commented match ignored",
			in:          "# Match user bob\nHost a\n  HostName 1.1.1.1\n",
			wantChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := sanitizeUnsupportedMatch([]byte(tt.in))
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if tt.wantChanged && string(got) != tt.want {
				t.Fatalf("got %q, want %q", string(got), tt.want)
			}
			if !tt.wantChanged && string(got) != tt.in {
				t.Fatalf("expected input unchanged, got %q", string(got))
			}
		})
	}
}
