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

	"github.com/kevinburke/ssh_config"
)

// maxIncludeDepth guards against include cycles and pathological nesting.
const maxIncludeDepth = 8

// expandIncludes flattens Include directives recursively.
//
// ssh_config keeps `Include` as an opaque node (and only resolves it for its own
// Get/GetAll helpers), so without this step every host defined in an included
// file — the common `Include ~/.ssh/config.d/*` layout — is simply invisible.
// Included hosts are returned in file order after the hosts of the including
// file, which matches how OpenSSH evaluates them.
func (r *Repository) expandIncludes(hosts []*ssh_config.Host, depth int) []*ssh_config.Host {
	if depth > maxIncludeDepth {
		return hosts
	}

	out := make([]*ssh_config.Host, 0, len(hosts))
	for _, host := range hosts {
		out = append(out, host)
		for _, node := range host.Nodes {
			inc, ok := node.(*ssh_config.Include)
			if !ok {
				continue
			}
			out = append(out, r.loadInclude(inc, depth+1)...)
		}
	}
	return out
}

// loadInclude resolves one Include node into the hosts it declares.
func (r *Repository) loadInclude(inc *ssh_config.Include, depth int) []*ssh_config.Host {
	directives := includePatterns(inc.String())
	var hosts []*ssh_config.Host
	for _, pattern := range directives {
		for _, path := range r.globInclude(pattern) {
			sub, err := r.decodeFile(path)
			if err != nil {
				r.logger.Warnw("failed to read included ssh config", "path", path, "error", err)
				continue
			}
			hosts = append(hosts, r.expandIncludes(sub.Hosts, depth)...)
		}
	}
	return hosts
}

// includePatterns extracts the file patterns from an Include node line such as
// `Include ~/.ssh/config.d/*` or `Include = a b`.
func includePatterns(line string) []string {
	fields := strings.Fields(strings.ReplaceAll(line, "=", " "))
	if len(fields) == 0 {
		return nil
	}
	if strings.EqualFold(fields[0], "include") {
		fields = fields[1:]
	}
	return fields
}

// globInclude expands one Include pattern into concrete file paths, honouring
// "~", relative-to-~/.ssh paths and negated patterns (`!pattern`).
func (r *Repository) globInclude(pattern string) []string {
	negated := strings.HasPrefix(pattern, "!")
	if negated {
		pattern = strings.TrimPrefix(pattern, "!")
	}

	pattern = strings.Trim(pattern, `"`)
	if pattern == "" {
		return nil
	}
	pattern = expandHome(pattern)
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(filepath.Dir(r.configPath), pattern)
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		r.logger.Warnw("invalid Include pattern", "pattern", pattern, "error", err)
		return nil
	}
	if !negated {
		return matches
	}

	excluded := make(map[string]bool, len(matches))
	for _, m := range matches {
		excluded[m] = true
	}
	all, _ := filepath.Glob(filepath.Join(filepath.Dir(r.configPath), "*"))
	var kept []string
	for _, p := range all {
		if !excluded[p] {
			kept = append(kept, p)
		}
	}
	return kept
}

// expandHome resolves a leading "~" using os.UserHomeDir.
func expandHome(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// decodeFile parses an arbitrary ssh config file (used for Include targets).
func (r *Repository) decodeFile(path string) (*ssh_config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh_config.DecodeBytes(data)
}
