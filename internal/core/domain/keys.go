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

// KeyGenOptions configures GenerateKey.
type KeyGenOptions struct {
	Path       string
	Type       string // ed25519 | rsa | ecdsa
	Comment    string
	Passphrase string
	Bits       int
	Overwrite  bool
}

// SSHKey describes a private key found under ~/.ssh.
type SSHKey struct {
	Path          string
	Type          string // ed25519, rsa, ecdsa… parsed from the .pub header
	Fingerprint   string // SHA256:… as reported by ssh-keygen -lf
	Bits          int
	Comment       string
	HasPublic     bool
	Protected     bool // passphrase protected
	ModTime       time.Time
	LoadedInAgent bool
}

// DisplayName returns the short label used in the key list.
func (k SSHKey) DisplayName() string {
	return k.Path
}

// KnownHostEntry is one known_hosts line (may cover several host patterns).
type KnownHostEntry struct {
	Line     int
	Hosts    string // raw first field, may be hashed (|1|…)
	KeyType  string
	Key      string
	Comment  string
	Hashed   bool
	Modified time.Time
}
