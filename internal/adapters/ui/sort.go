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

package ui

import (
	"sort"
	"strings"

	"github.com/linpr/lazyssh/internal/core/domain"
)

// SortMode is one of the orderings the list can be sorted by.
type SortMode int

const (
	SortAliasAsc SortMode = iota
	SortAliasDesc
	SortLastSeenDesc
	SortMostUsed
	SortModeCount
)

// String is the label shown in the status bar.
func (s SortMode) String() string {
	switch s {
	case SortAliasAsc:
		return "sort: alias A→Z"
	case SortAliasDesc:
		return "sort: alias Z→A"
	case SortLastSeenDesc:
		return "sort: last used"
	case SortMostUsed:
		return "sort: most used"
	}
	return "sort: ?"
}

// Next cycles to the next mode.
func (s SortMode) Next() SortMode {
	return (s + 1) % SortModeCount
}

// sortServers orders servers for display. Pinned hosts always bubble to the top
// regardless of the chosen mode, which matches the previous hard-coded rule and
// keeps the pin useful when the user changes the sort order.
func sortServers(servers []domain.Server, mode SortMode) {
	sort.SliceStable(servers, func(i, j int) bool {
		a, b := servers[i], servers[j]
		ap, bp := !a.PinnedAt.IsZero(), !b.PinnedAt.IsZero()
		if ap != bp {
			return ap
		}
		if ap && bp && !a.PinnedAt.Equal(b.PinnedAt) {
			return a.PinnedAt.After(b.PinnedAt)
		}

		switch mode {
		case SortAliasAsc:
			return strings.ToLower(a.Alias) < strings.ToLower(b.Alias)
		case SortAliasDesc:
			return strings.ToLower(a.Alias) > strings.ToLower(b.Alias)
		case SortLastSeenDesc:
			if !a.LastSeen.Equal(b.LastSeen) {
				return a.LastSeen.After(b.LastSeen)
			}
			return strings.ToLower(a.Alias) < strings.ToLower(b.Alias)
		case SortMostUsed:
			if a.SSHCount != b.SSHCount {
				return a.SSHCount > b.SSHCount
			}
			return strings.ToLower(a.Alias) < strings.ToLower(b.Alias)
		}
		return false
	})
}
