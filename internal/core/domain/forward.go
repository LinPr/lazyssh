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

package domain

import "time"

// ForwardKind identifies which ssh forwarding flag a spec maps to.
type ForwardKind string

const (
	ForwardLocal   ForwardKind = "local"   // -L
	ForwardRemote  ForwardKind = "remote"  // -R
	ForwardDynamic ForwardKind = "dynamic" // -D
)

// ForwardRule is a single port-forwarding rule belonging to a host.
type ForwardRule struct {
	Kind ForwardKind
	Spec string // ssh CLI form, e.g. "8080:localhost:80" or "1080"
}

// Flag renders the rule as the ssh command-line arguments it needs.
func (f ForwardRule) Flag() []string {
	switch f.Kind {
	case ForwardLocal:
		return []string{"-L", f.Spec}
	case ForwardRemote:
		return []string{"-R", f.Spec}
	case ForwardDynamic:
		return []string{"-D", f.Spec}
	}
	return nil
}

// String is the human readable form shown in the UI.
func (f ForwardRule) String() string {
	return string(f.Kind) + " " + f.Spec
}

// ForwardStatus describes the live state of one host's forwarding process.
type ForwardStatus struct {
	Alias     string
	Rules     []ForwardRule
	PID       int
	StartedAt time.Time
	Running   bool
	Err       error
}

// Duration returns how long the forward has been running.
func (s ForwardStatus) Duration() time.Duration {
	if s.StartedAt.IsZero() {
		return 0
	}
	return time.Since(s.StartedAt)
}
