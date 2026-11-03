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
	"fmt"
	"strings"

	"github.com/kevinburke/ssh_config"
	"github.com/linpr/lazyssh/internal/core/domain"
)

const (
	MaxBackups         = 10
	TempSuffix         = ".tmp"
	BackupSuffix       = "lazyssh.backup"
	SSHConfigPerms     = 0o600
	OriginalBackupName = "config.original.backup"

	// kvIndent is the indentation applied to key/value lines that lazyssh
	// creates itself. Parsed lines keep whatever indentation they had.
	kvIndent = "    "
)

// filterServers filters servers against a query that may mix plain terms and
// "#tag" filters, e.g. "prod #web". All terms must match (AND semantics) and
// matching is case-insensitive across alias, host, user and tags.
//
// The tag syntax previously only worked in one UI code path while the repository
// used a different rule, so the same query could return different rows depending
// on how it reached the backend.
func (r *Repository) filterServers(servers []domain.Server, query string) []domain.Server {
	terms, tags := splitQuery(query)
	if len(terms) == 0 && len(tags) == 0 {
		return servers
	}

	filtered := make([]domain.Server, 0, len(servers))
	for _, server := range servers {
		if !matchesTags(server, tags) {
			continue
		}
		if !matchesTerms(server, terms) {
			continue
		}
		filtered = append(filtered, server)
	}
	return filtered
}

// splitQuery separates "#tag" tokens from free-text terms.
func splitQuery(query string) (terms, tags []string) {
	for _, tok := range strings.Fields(strings.ToLower(query)) {
		if strings.HasPrefix(tok, "#") {
			if tag := strings.TrimPrefix(tok, "#"); tag != "" {
				tags = append(tags, tag)
			}
			continue
		}
		terms = append(terms, tok)
	}
	return terms, tags
}

// matchesTags reports whether the server carries every requested tag.
func matchesTags(server domain.Server, tags []string) bool {
	for _, want := range tags {
		found := false
		for _, have := range server.Tags {
			if strings.EqualFold(have, want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// matchesTerms reports whether every free-text term appears in a searchable field.
func matchesTerms(server domain.Server, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		server.Alias,
		strings.Join(server.Aliases, " "),
		server.Host,
		server.User,
		strings.Join(server.Tags, " "),
	}, " "))

	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

// serverExists checks if a server with the given alias already exists in the config.
func (r *Repository) serverExists(cfg *ssh_config.Config, alias string) bool {
	return r.findHostByAlias(cfg, alias) != nil
}

// findHostByAlias finds a host by its alias in the SSH config.
func (r *Repository) findHostByAlias(cfg *ssh_config.Config, alias string) *ssh_config.Host {
	for _, host := range cfg.Hosts {
		if r.hostContainsPattern(host, alias) {
			return host
		}
	}
	return nil
}

// hostContainsPattern checks if a host contains a specific pattern.
func (r *Repository) hostContainsPattern(host *ssh_config.Host, target string) bool {
	for _, pattern := range host.Patterns {
		if pattern.String() == target {
			return true
		}
	}
	return false
}

// createHostFromServer creates a new ssh_config.Host from a domain.Server.
func (r *Repository) createHostFromServer(server domain.Server) (*ssh_config.Host, error) {
	// ssh_config.Pattern can only be built through NewPattern (upstream ≥1.5
	// made its fields unexported).
	pattern, err := ssh_config.NewPattern(server.Alias)
	if err != nil {
		return nil, fmt.Errorf("invalid alias %q: %w", server.Alias, err)
	}
	host := &ssh_config.Host{
		Patterns:   []*ssh_config.Pattern{pattern},
		Nodes:      make([]ssh_config.Node, 0),
		EOLComment: "Added by lazyssh",
	}

	// Basic config - always present
	r.addKVNodeIfNotEmpty(host, "HostName", server.Host)
	r.addKVNodeIfNotEmpty(host, "User", server.User)
	if server.Port != 0 {
		r.addKVNodeIfNotEmpty(host, "Port", fmt.Sprintf("%d", server.Port))
	}
	for _, identityFile := range server.IdentityFiles {
		r.addKVNodeIfNotEmpty(host, "IdentityFile", identityFile)
	}

	// Connection and proxy settings
	r.addKVNodeIfNotEmpty(host, "ProxyJump", server.ProxyJump)
	r.addKVNodeIfNotEmpty(host, "ProxyCommand", server.ProxyCommand)
	r.addKVNodeIfNotEmpty(host, "RemoteCommand", server.RemoteCommand)
	r.addKVNodeIfNotEmpty(host, "RequestTTY", server.RequestTTY)
	r.addKVNodeIfNotEmpty(host, "ConnectTimeout", server.ConnectTimeout)
	r.addKVNodeIfNotEmpty(host, "ConnectionAttempts", server.ConnectionAttempts)

	// Port forwarding
	for _, forward := range server.LocalForward {
		configFormat := r.convertCLIForwardToConfigFormat(forward)
		r.addKVNodeIfNotEmpty(host, "LocalForward", configFormat)
	}
	for _, forward := range server.RemoteForward {
		configFormat := r.convertCLIForwardToConfigFormat(forward)
		r.addKVNodeIfNotEmpty(host, "RemoteForward", configFormat)
	}
	for _, forward := range server.DynamicForward {
		r.addKVNodeIfNotEmpty(host, "DynamicForward", forward)
	}

	// Authentication and key management
	r.addKVNodeIfNotEmpty(host, "PubkeyAuthentication", server.PubkeyAuthentication)
	r.addKVNodeIfNotEmpty(host, "PubkeyAcceptedAlgorithms", server.PubkeyAcceptedAlgorithms)
	r.addKVNodeIfNotEmpty(host, "HostbasedAcceptedAlgorithms", server.HostbasedAcceptedAlgorithms)
	r.addKVNodeIfNotEmpty(host, "PasswordAuthentication", server.PasswordAuthentication)
	r.addKVNodeIfNotEmpty(host, "PreferredAuthentications", server.PreferredAuthentications)
	r.addKVNodeIfNotEmpty(host, "IdentitiesOnly", server.IdentitiesOnly)
	r.addKVNodeIfNotEmpty(host, "AddKeysToAgent", server.AddKeysToAgent)
	r.addKVNodeIfNotEmpty(host, "IdentityAgent", server.IdentityAgent)

	// Agent and X11 forwarding
	r.addKVNodeIfNotEmpty(host, "ForwardAgent", server.ForwardAgent)
	r.addKVNodeIfNotEmpty(host, "ForwardX11", server.ForwardX11)
	r.addKVNodeIfNotEmpty(host, "ForwardX11Trusted", server.ForwardX11Trusted)

	// Connection multiplexing
	r.addKVNodeIfNotEmpty(host, "ControlMaster", server.ControlMaster)
	r.addKVNodeIfNotEmpty(host, "ControlPath", server.ControlPath)
	r.addKVNodeIfNotEmpty(host, "ControlPersist", server.ControlPersist)

	// Connection reliability
	r.addKVNodeIfNotEmpty(host, "ServerAliveInterval", server.ServerAliveInterval)
	r.addKVNodeIfNotEmpty(host, "ServerAliveCountMax", server.ServerAliveCountMax)
	r.addKVNodeIfNotEmpty(host, "Compression", server.Compression)
	r.addKVNodeIfNotEmpty(host, "TCPKeepAlive", server.TCPKeepAlive)
	r.addKVNodeIfNotEmpty(host, "BatchMode", server.BatchMode)

	// Security
	r.addKVNodeIfNotEmpty(host, "StrictHostKeyChecking", server.StrictHostKeyChecking)
	r.addKVNodeIfNotEmpty(host, "UserKnownHostsFile", server.UserKnownHostsFile)
	r.addKVNodeIfNotEmpty(host, "HostKeyAlgorithms", server.HostKeyAlgorithms)
	r.addKVNodeIfNotEmpty(host, "VerifyHostKeyDNS", server.VerifyHostKeyDNS)
	r.addKVNodeIfNotEmpty(host, "UpdateHostKeys", server.UpdateHostKeys)
	r.addKVNodeIfNotEmpty(host, "HashKnownHosts", server.HashKnownHosts)
	r.addKVNodeIfNotEmpty(host, "VisualHostKey", server.VisualHostKey)

	// Command execution
	r.addKVNodeIfNotEmpty(host, "LocalCommand", server.LocalCommand)
	r.addKVNodeIfNotEmpty(host, "PermitLocalCommand", server.PermitLocalCommand)
	r.addKVNodeIfNotEmpty(host, "EscapeChar", server.EscapeChar)

	// Environment settings
	for _, env := range server.SendEnv {
		r.addKVNodeIfNotEmpty(host, "SendEnv", env)
	}
	for _, env := range server.SetEnv {
		r.addKVNodeIfNotEmpty(host, "SetEnv", env)
	}

	// Debugging
	r.addKVNodeIfNotEmpty(host, "LogLevel", server.LogLevel)

	return host, nil
}

// addKVNodeIfNotEmpty adds a key-value node to the host if the value is not empty.
//
// Upstream ssh_config (≥1.5) made KV's indentation field unexported, so the
// four-space indent that the previous fork expressed via LeadingSpace is part of
// the key itself here.
func (r *Repository) addKVNodeIfNotEmpty(host *ssh_config.Host, key, value string) {
	if value == "" {
		return
	}

	kvNode := &ssh_config.KV{
		Key:   kvIndent + key,
		Value: value,
	}
	r.insertKVNodeAfterLastKV(host, kvNode)
}

// insertKVNodeAfterLastKV inserts a KV node immediately after the last existing KV node in the host.
// This preserves any trailing non-KV nodes (blank lines, comments) that may exist after the host's
// configuration block, preventing formatting shifts when adding new fields to a host entry.
//
// Example: If a host block has trailing blank lines separating it from the next host entry,
// this function ensures new fields are inserted before those blank lines, maintaining the
// visual separation between host blocks.
func (r *Repository) insertKVNodeAfterLastKV(host *ssh_config.Host, kvNode *ssh_config.KV) {
	// STEP 1: Find the last KV node (search backwards)
	lastKVIndex := -1
	for i := len(host.Nodes) - 1; i >= 0; i-- {
		if _, ok := host.Nodes[i].(*ssh_config.KV); ok {
			lastKVIndex = i
			break
		}
	}

	// STEP 2: Handle case where no KV nodes exist
	if lastKVIndex == -1 {
		if len(host.Nodes) == 0 {
			// Case A: Empty host - just append
			host.Nodes = append(host.Nodes, kvNode)
		} else {
			// Case B: Only comments/blanks exist - prepend before them
			host.Nodes = append([]ssh_config.Node{kvNode}, host.Nodes...)
		}
		return
	}

	// STEP 3: We found KV nodes - insert after the last one
	insertAt := lastKVIndex + 1

	if insertAt == len(host.Nodes) {
		// Case C: Last KV is at the end - just append
		host.Nodes = append(host.Nodes, kvNode)
		return
	}

	// Case D: Last KV has trailing nodes (blanks/comments) - insert between them
	host.Nodes = append(host.Nodes[:insertAt], append([]ssh_config.Node{kvNode}, host.Nodes[insertAt:]...)...)
}

// matchesKVKey reports whether an ssh_config KV node holds the given directive
// name. Parsed nodes carry the raw key text (leading indentation included),
// while lazyssh-created nodes use kvIndent, so both are trimmed before comparing.
func matchesKVKey(kv *ssh_config.KV, key string) bool {
	return strings.EqualFold(strings.TrimSpace(kv.Key), key)
}

// removeNodesByKey removes all nodes with the specified key from the nodes slice
func removeNodesByKey(nodes []ssh_config.Node, key string) []ssh_config.Node {
	filtered := make([]ssh_config.Node, 0, len(nodes))
	for _, node := range nodes {
		if kv, ok := node.(*ssh_config.KV); ok {
			if matchesKVKey(kv, key) {
				continue // skip nodes with matching key
			}
		}
		filtered = append(filtered, node)
	}
	return filtered
}

// updateHostNodes updates the nodes of an existing host with new server details.
func (r *Repository) updateHostNodes(host *ssh_config.Host, newServer domain.Server) {
	// Handle Port - include if explicitly set (even if it's 22)
	portValue := ""
	if newServer.Port != 0 {
		portValue = fmt.Sprintf("%d", newServer.Port)
	}

	updates := map[string]string{
		"hostname":                        newServer.Host,
		"user":                            newServer.User,
		"port":                            portValue,
		"proxycommand":                    newServer.ProxyCommand,
		"proxyjump":                       newServer.ProxyJump,
		"remotecommand":                   newServer.RemoteCommand,
		"requesttty":                      newServer.RequestTTY,
		"sessiontype":                     newServer.SessionType,
		"connecttimeout":                  newServer.ConnectTimeout,
		"connectionattempts":              newServer.ConnectionAttempts,
		"bindaddress":                     newServer.BindAddress,
		"bindinterface":                   newServer.BindInterface,
		"addressfamily":                   newServer.AddressFamily,
		"exitonforwardfailure":            newServer.ExitOnForwardFailure,
		"ipqos":                           newServer.IPQoS,
		"canonicalizehostname":            newServer.CanonicalizeHostname,
		"canonicaldomains":                newServer.CanonicalDomains,
		"canonicalizefallbacklocal":       newServer.CanonicalizeFallbackLocal,
		"canonicalizemaxdots":             newServer.CanonicalizeMaxDots,
		"canonicalizepermittedcnames":     newServer.CanonicalizePermittedCNAMEs,
		"clearallforwardings":             newServer.ClearAllForwardings,
		"gatewayports":                    newServer.GatewayPorts,
		"pubkeyauthentication":            newServer.PubkeyAuthentication,
		"passwordauthentication":          newServer.PasswordAuthentication,
		"preferredauthentications":        newServer.PreferredAuthentications,
		"pubkeyacceptedalgorithms":        newServer.PubkeyAcceptedAlgorithms,
		"pubkeyacceptedkeytypes":          newServer.PubkeyAcceptedAlgorithms, // Deprecated alias (since OpenSSH 8.5)
		"hostbasedacceptedalgorithms":     newServer.HostbasedAcceptedAlgorithms,
		"hostbasedkeytypes":               newServer.HostbasedAcceptedAlgorithms, // Deprecated alias (since OpenSSH 8.5)
		"hostbasedacceptedkeytypes":       newServer.HostbasedAcceptedAlgorithms, // Deprecated alias (since OpenSSH 8.5)
		"identitiesonly":                  newServer.IdentitiesOnly,
		"addkeystoagent":                  newServer.AddKeysToAgent,
		"identityagent":                   newServer.IdentityAgent,
		"kbdinteractiveauthentication":    newServer.KbdInteractiveAuthentication,
		"challengeresponseauthentication": newServer.KbdInteractiveAuthentication, // Deprecated alias
		"numberofpasswordprompts":         newServer.NumberOfPasswordPrompts,
		"forwardagent":                    newServer.ForwardAgent,
		"forwardx11":                      newServer.ForwardX11,
		"forwardx11trusted":               newServer.ForwardX11Trusted,
		"controlmaster":                   newServer.ControlMaster,
		"controlpath":                     newServer.ControlPath,
		"controlpersist":                  newServer.ControlPersist,
		"serveraliveinterval":             newServer.ServerAliveInterval,
		"serveralivecountmax":             newServer.ServerAliveCountMax,
		"compression":                     newServer.Compression,
		"tcpkeepalive":                    newServer.TCPKeepAlive,
		"batchmode":                       newServer.BatchMode,
		"stricthostkeychecking":           newServer.StrictHostKeyChecking,
		"checkhostip":                     newServer.CheckHostIP,
		"fingerprinthash":                 newServer.FingerprintHash,
		"userknownhostsfile":              newServer.UserKnownHostsFile,
		"hostkeyalgorithms":               newServer.HostKeyAlgorithms,
		"macs":                            newServer.MACs,
		"ciphers":                         newServer.Ciphers,
		"kexalgorithms":                   newServer.KexAlgorithms,
		"verifyhostkeydns":                newServer.VerifyHostKeyDNS,
		"updatehostkeys":                  newServer.UpdateHostKeys,
		"hashknownhosts":                  newServer.HashKnownHosts,
		"visualhostkey":                   newServer.VisualHostKey,
		"localcommand":                    newServer.LocalCommand,
		"permitlocalcommand":              newServer.PermitLocalCommand,
		"escapechar":                      newServer.EscapeChar,
		"loglevel":                        newServer.LogLevel,
	}

	// Update or remove nodes based on value
	for key, value := range updates {
		if value != "" {
			r.updateOrAddKVNode(host, key, value)
		} else {
			// Remove the key if value is empty (user selected default)
			r.removeKVNode(host, key)
		}
	}

	// Replace multi-value entries entirely to reflect the new state
	host.Nodes = removeNodesByKey(host.Nodes, "IdentityFile")
	for _, identityFile := range newServer.IdentityFiles {
		r.addKVNodeIfNotEmpty(host, "IdentityFile", identityFile)
	}

	host.Nodes = removeNodesByKey(host.Nodes, "LocalForward")
	for _, forward := range newServer.LocalForward {
		configFormat := r.convertCLIForwardToConfigFormat(forward)
		r.addKVNodeIfNotEmpty(host, "LocalForward", configFormat)
	}

	host.Nodes = removeNodesByKey(host.Nodes, "RemoteForward")
	for _, forward := range newServer.RemoteForward {
		configFormat := r.convertCLIForwardToConfigFormat(forward)
		r.addKVNodeIfNotEmpty(host, "RemoteForward", configFormat)
	}

	host.Nodes = removeNodesByKey(host.Nodes, "DynamicForward")
	for _, forward := range newServer.DynamicForward {
		r.addKVNodeIfNotEmpty(host, "DynamicForward", forward)
	}

	host.Nodes = removeNodesByKey(host.Nodes, "SendEnv")
	for _, env := range newServer.SendEnv {
		r.addKVNodeIfNotEmpty(host, "SendEnv", env)
	}

	host.Nodes = removeNodesByKey(host.Nodes, "SetEnv")
	for _, env := range newServer.SetEnv {
		r.addKVNodeIfNotEmpty(host, "SetEnv", env)
	}
}

// updateOrAddKVNode updates an existing key-value node or adds a new one if it doesn't exist.
//
// Parsed nodes keep their original raw value text for faithful round-tripping
// (ssh_config ≥1.5), so mutating KV.Value in place is not enough to change what
// gets written back — the node has to be replaced with a freshly built one.
func (r *Repository) updateOrAddKVNode(host *ssh_config.Host, key, newValue string) {
	for i, node := range host.Nodes {
		kvNode, ok := node.(*ssh_config.KV)
		if ok && matchesKVKey(kvNode, key) {
			host.Nodes[i] = &ssh_config.KV{
				Key:   kvIndent + r.getProperKeyCase(key),
				Value: newValue,
			}
			return
		}
	}

	// Add new node if not found
	kvNode := &ssh_config.KV{
		Key:   kvIndent + r.getProperKeyCase(key),
		Value: newValue,
	}
	r.insertKVNodeAfterLastKV(host, kvNode)
}

// removeKVNode removes a key-value node from the host if it exists.
func (r *Repository) removeKVNode(host *ssh_config.Host, key string) {
	filtered := make([]ssh_config.Node, 0, len(host.Nodes))
	for _, node := range host.Nodes {
		if kvNode, ok := node.(*ssh_config.KV); ok {
			if matchesKVKey(kvNode, key) {
				continue // Skip this node (remove it)
			}
		}
		filtered = append(filtered, node)
	}
	host.Nodes = filtered
}

// getProperKeyCase returns the proper case for known SSH config keys.
// Reference: https://www.ssh.com/academy/ssh/config
func (r *Repository) getProperKeyCase(key string) string {
	keyMap := map[string]string{
		"hostname":                        "HostName",
		"user":                            "User",
		"port":                            "Port",
		"identityfile":                    "IdentityFile",
		"proxycommand":                    "ProxyCommand",
		"proxyjump":                       "ProxyJump",
		"remotecommand":                   "RemoteCommand",
		"requesttty":                      "RequestTTY",
		"sessiontype":                     "SessionType",
		"connecttimeout":                  "ConnectTimeout",
		"connectionattempts":              "ConnectionAttempts",
		"bindaddress":                     "BindAddress",
		"bindinterface":                   "BindInterface",
		"addressfamily":                   "AddressFamily",
		"exitonforwardfailure":            "ExitOnForwardFailure",
		"ipqos":                           "IPQoS",
		"canonicalizehostname":            "CanonicalizeHostname",
		"canonicaldomains":                "CanonicalDomains",
		"canonicalizefallbacklocal":       "CanonicalizeFallbackLocal",
		"canonicalizemaxdots":             "CanonicalizeMaxDots",
		"canonicalizepermittedcnames":     "CanonicalizePermittedCNAMEs",
		"localforward":                    "LocalForward",
		"remoteforward":                   "RemoteForward",
		"dynamicforward":                  "DynamicForward",
		"clearallforwardings":             "ClearAllForwardings",
		"gatewayports":                    "GatewayPorts",
		"pubkeyauthentication":            "PubkeyAuthentication",
		"passwordauthentication":          "PasswordAuthentication",
		"preferredauthentications":        "PreferredAuthentications",
		"pubkeyacceptedalgorithms":        "PubkeyAcceptedAlgorithms",
		"pubkeyacceptedkeytypes":          "PubkeyAcceptedAlgorithms", // Deprecated alias (since OpenSSH 8.5)
		"hostbasedacceptedalgorithms":     "HostbasedAcceptedAlgorithms",
		"hostbasedkeytypes":               "HostbasedAcceptedAlgorithms", // Deprecated alias (since OpenSSH 8.5)
		"hostbasedacceptedkeytypes":       "HostbasedAcceptedAlgorithms", // Deprecated alias (since OpenSSH 8.5)
		"identitiesonly":                  "IdentitiesOnly",
		"addkeystoagent":                  "AddKeysToAgent",
		"identityagent":                   "IdentityAgent",
		"kbdinteractiveauthentication":    "KbdInteractiveAuthentication",
		"challengeresponseauthentication": "KbdInteractiveAuthentication", // Deprecated alias
		"numberofpasswordprompts":         "NumberOfPasswordPrompts",
		"forwardagent":                    "ForwardAgent",
		"forwardx11":                      "ForwardX11",
		"forwardx11trusted":               "ForwardX11Trusted",
		"controlmaster":                   "ControlMaster",
		"controlpath":                     "ControlPath",
		"controlpersist":                  "ControlPersist",
		"serveraliveinterval":             "ServerAliveInterval",
		"serveralivecountmax":             "ServerAliveCountMax",
		"compression":                     "Compression",
		"tcpkeepalive":                    "TCPKeepAlive",
		"stricthostkeychecking":           "StrictHostKeyChecking",
		"checkhostip":                     "CheckHostIP",
		"fingerprinthash":                 "FingerprintHash",
		"verifyhostkeydns":                "VerifyHostKeyDNS",
		"updatehostkeys":                  "UpdateHostKeys",
		"hashknownhosts":                  "HashKnownHosts",
		"visualhostkey":                   "VisualHostKey",
		"userknownhostsfile":              "UserKnownHostsFile",
		"hostkeyalgorithms":               "HostKeyAlgorithms",
		"macs":                            "MACs",
		"ciphers":                         "Ciphers",
		"kexalgorithms":                   "KexAlgorithms",
		"localcommand":                    "LocalCommand",
		"permitlocalcommand":              "PermitLocalCommand",
		"escapechar":                      "EscapeChar",
		"sendenv":                         "SendEnv",
		"setenv":                          "SetEnv",
		"loglevel":                        "LogLevel",
		"batchmode":                       "BatchMode",
	}

	if properCase, exists := keyMap[strings.ToLower(key)]; exists {
		return properCase
	}
	return key
}

// convertCLIForwardToConfigFormat converts CLI format forwarding spec to SSH config format.
// CLI format: [bind_address:]port:host:hostport
// Config format: [bind_address:]port host:hostport
func (r *Repository) convertCLIForwardToConfigFormat(forward string) string {
	// Handle IPv6 addresses in brackets like [2001:db8::1]
	// These should be treated as a single unit

	// Find the last `:digits` that represents the final port
	lastPortStart := -1
	for i := len(forward) - 1; i >= 0; i-- {
		if forward[i] == ':' {
			// Check if everything after this colon is digits
			if i+1 < len(forward) {
				allDigits := true
				hasDigits := false
				for j := i + 1; j < len(forward); j++ {
					if forward[j] >= '0' && forward[j] <= '9' {
						hasDigits = true
					} else {
						allDigits = false
						break
					}
				}
				if allDigits && hasDigits {
					lastPortStart = i
					break
				}
			}
		}
	}

	if lastPortStart == -1 {
		// No port at the end, return as-is
		return forward
	}

	// Now find the split point between local and remote parts
	// We need to handle bracket-enclosed addresses specially
	inBrackets := 0
	for i := lastPortStart - 1; i >= 0; i-- {
		switch forward[i] {
		case ']':
			inBrackets++
		case '[':
			inBrackets--
		case ':':
			if inBrackets != 0 {
				continue
			}
			// This colon is not inside brackets
			// Check if this looks like it could be the split point
			// The split point would be after a port number (digits after a colon)

			// Look ahead to see what comes after this colon
			nextChar := byte(' ')
			if i+1 < len(forward) {
				nextChar = forward[i+1]
			}

			// If the next character could be start of a host (letter, digit, bracket)
			// then this is our split point
			if nextChar != ':' {
				localPart := forward[:i]
				remotePart := forward[i+1:]
				return localPart + " " + remotePart
			}
		}
	}

	// If no split point found, return as-is
	return forward
}

// removeHostByAlias removes a host by its alias from the list of hosts.
func (r *Repository) removeHostByAlias(hosts []*ssh_config.Host, alias string) []*ssh_config.Host {
	for i, host := range hosts {
		if r.hostContainsPattern(host, alias) {
			return append(hosts[:i], hosts[i+1:]...)
		}
	}
	return hosts
}
