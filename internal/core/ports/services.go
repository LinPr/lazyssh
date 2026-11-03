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

package ports

import (
	"time"

	"github.com/linpr/lazyssh/internal/core/domain"
)

// TransferProgress carries the current state of a file transfer.
type TransferProgress struct {
	Percent int
	Bytes   int64
	Total   int64
	Done    bool
	Err     error
}

// ServerService is the application-facing API consumed by the TUI and CLI.
type ServerService interface {
	// Config CRUD.
	ListServers(query string) ([]domain.Server, error)
	UpdateServer(server domain.Server, newServer domain.Server) error
	AddServer(server domain.Server) error
	DeleteServer(server domain.Server) error
	SetPinned(alias string, pinned bool) error
	RecordConnect(alias string) error
	GetRawConfigBlock(alias string) (string, error)
	ValidateConfig() ([]string, error)
	ConfigPath() string

	// Connectivity.
	Ping(server domain.Server) (bool, time.Duration, error)
	ResolveDestination(alias string) (host string, port int, err error)

	// Interactive sessions.
	SSH(alias string) error
	SSHWithArgs(alias string, extraArgs []string) error

	// Port forwarding.
	ForwardRules(alias string) []domain.ForwardRule
	StartForward(alias string, rules []domain.ForwardRule, extraArgs []string) (int, error)
	StopForwarding(alias string) error
	IsForwarding(alias string) bool
	ForwardStatuses() []domain.ForwardStatus

	// ControlMaster lifecycle.
	EstablishSSHMaster(alias string) error
	CloseSSHMaster(alias string) error
	SSHMasterExists(alias string) bool
	TrySSHMasterBatch(alias string) error
	EstablishSSHMasterWithPassword(alias, password string) error

	// SFTP.
	ListRemoteFiles(alias, path string) ([]domain.FileEntry, error)
	MkdirRemote(alias, path string) error
	DeleteRemote(alias, path string) error
	RenameRemote(alias, oldPath, newPath string) error
	ChmodRemote(alias, path string, mode uint32) error
	HomeDirRemote(alias string) (string, error)
	UploadFile(alias, localPath, remotePath string, progress chan<- TransferProgress) error
	DownloadFile(alias, remotePath, localPath string, progress chan<- TransferProgress) error

	// Key management.
	ListSSHKeys() ([]domain.SSHKey, error)
	AgentKeys() ([]domain.SSHKey, error)
	AddKeyToAgent(path string) error
	GenerateKey(opts domain.KeyGenOptions) (domain.SSHKey, error)
	CopyKeyToHost(path, alias string) error

	// known_hosts.
	ListKnownHosts() ([]domain.KnownHostEntry, error)
	RemoveKnownHost(host string) (int, error)
}
