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
	"sync/atomic"

	"github.com/kevinburke/ssh_config"
	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/linpr/lazyssh/internal/core/ports"
	"go.uber.org/zap"
)

// Repository implements ServerRepository interface for SSH config file operations.
type Repository struct {
	configPath      string
	fileSystem      FileSystem
	metadataManager *metadataManager
	logger          *zap.SugaredLogger

	// readOnly is set when the config could not be parsed losslessly; writes are
	// refused so that lazyssh never corrupts a hand-written ~/.ssh/config.
	readOnly atomic.Bool
}

// NewRepository creates a new SSH config repository.
func NewRepository(logger *zap.SugaredLogger, configPath, metaDataPath string) ports.ServerRepository {
	return &Repository{
		logger:          logger,
		configPath:      configPath,
		fileSystem:      DefaultFileSystem{},
		metadataManager: newMetadataManager(metaDataPath, logger),
	}
}

// NewRepositoryWithFS creates a new SSH config repository with a custom filesystem.
func NewRepositoryWithFS(logger *zap.SugaredLogger, configPath string, metaDataPath string, fs FileSystem) ports.ServerRepository {
	return &Repository{
		logger:          logger,
		configPath:      configPath,
		fileSystem:      fs,
		metadataManager: newMetadataManager(metaDataPath, logger),
	}
}

// ListServers returns all servers matching the query pattern.
// Empty query returns all servers.
func (r *Repository) ListServers(query string) ([]domain.Server, error) {
	cfg, err := r.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Hosts pulled in through Include are part of the user's real config, so they
	// belong in the list even though the top-level file never declares them.
	hosts := r.expandIncludes(cfg.Hosts, 0)
	servers := make([]domain.Server, 0, len(hosts))
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		srv, ok := r.hostToServer(host)
		if !ok {
			continue
		}
		// OpenSSH lets a later block override an earlier one; keep the first
		// concrete definition so the list has exactly one row per alias.
		if seen[srv.Alias] {
			continue
		}
		seen[srv.Alias] = true
		servers = append(servers, srv)
	}

	metadata, err := r.metadataManager.loadAll()
	if err != nil {
		r.logger.Warnf("Failed to load metadata: %v", err)
		metadata = make(map[string]ServerMetadata)
	}
	servers = r.mergeMetadata(servers, metadata)
	if query == "" {
		return servers, nil
	}

	return r.filterServers(servers, query), nil
}

// AddServer adds a new server to the SSH config.
func (r *Repository) AddServer(server domain.Server) error {
	cfg, err := r.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if r.serverExists(cfg, server.Alias) {
		return fmt.Errorf("server with alias '%s' already exists", server.Alias)
	}

	host, err := r.createHostFromServer(server)
	if err != nil {
		return err
	}
	cfg.Hosts = append(cfg.Hosts, host)

	if err := r.saveConfig(cfg); err != nil {
		r.logger.Warnf("Failed to save config while adding new server: %v", err)
		return fmt.Errorf("failed to save config: %w", err)
	}
	return r.metadataManager.updateServer(server, server.Alias)
}

// UpdateServer updates an existing server in the SSH config.
func (r *Repository) UpdateServer(server domain.Server, newServer domain.Server) error {
	cfg, err := r.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	host := r.findHostByAlias(cfg, server.Alias)
	if host == nil {
		return fmt.Errorf("server with alias '%s' not found", server.Alias)
	}

	if server.Alias != newServer.Alias {
		if r.serverExists(cfg, newServer.Alias) {
			return fmt.Errorf("server with alias '%s' already exists", newServer.Alias)
		}

		newPatterns := make([]*ssh_config.Pattern, 0, len(host.Patterns))
		for _, pattern := range host.Patterns {
			if pattern.String() == server.Alias {
				// ssh_config.Pattern values are immutable upstream (≥1.5), so a
				// replacement pattern has to be constructed via NewPattern.
				replacement, err := ssh_config.NewPattern(newServer.Alias)
				if err != nil {
					return fmt.Errorf("invalid alias '%s': %w", newServer.Alias, err)
				}
				newPatterns = append(newPatterns, replacement)
			} else {
				newPatterns = append(newPatterns, pattern)
			}
		}

		host.Patterns = newPatterns

	}

	r.updateHostNodes(host, newServer)

	if err := r.saveConfig(cfg); err != nil {
		r.logger.Warnf("Failed to save config while updating server: %v", err)
		return fmt.Errorf("failed to save config: %w", err)
	}
	// Update metadata; pass old alias to allow inline migration
	return r.metadataManager.updateServer(newServer, server.Alias)
}

// DeleteServer removes a server from the SSH config.
func (r *Repository) DeleteServer(server domain.Server) error {
	cfg, err := r.loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	initialCount := len(cfg.Hosts)
	cfg.Hosts = r.removeHostByAlias(cfg.Hosts, server.Alias)

	if len(cfg.Hosts) == initialCount {
		return fmt.Errorf("server with alias '%s' not found", server.Alias)
	}

	if err := r.saveConfig(cfg); err != nil {
		r.logger.Warnf("Failed to save config while deleting server: %v", err)
		return fmt.Errorf("failed to save config: %w", err)
	}
	return r.metadataManager.deleteServer(server.Alias)
}

// SetPinned sets or unsets the pinned status of a server.
func (r *Repository) SetPinned(alias string, pinned bool) error {
	return r.metadataManager.setPinned(alias, pinned)
}

// RecordSSH increments the SSH access count and updates the last seen timestamp for a server.
func (r *Repository) RecordSSH(alias string) error {
	return r.metadataManager.recordSSH(alias)
}
