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
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"

	"github.com/linpr/lazyssh/internal/core/domain"
	"github.com/linpr/lazyssh/internal/core/ports"
	"github.com/pkg/sftp"
)

// openSFTP starts the sftp subsystem over the existing ControlMaster socket and
// returns a ready-to-use sftp.Client. The returned cleanup func must always be
// called (typically via defer) to close the client and reap the underlying process.
func openSFTP(alias string) (*sftp.Client, func(), error) {
	cp := controlPath(alias)
	// -s sftp requests the sftp-server subsystem on the remote over the existing
	// ControlMaster connection — no re-authentication required.
	// #nosec G204
	cmd := exec.Command("ssh", "-o", "ControlPath="+cp, alias, "-s", "sftp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("sftp pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("sftp pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("sftp start: %w", err)
	}
	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("sftp handshake: %w", err)
	}
	cleanup := func() {
		client.Close()
		_ = cmd.Wait()
	}
	return client, cleanup, nil
}

// sftpRemoveAll recursively removes path (file or directory tree) via SFTP.
func sftpRemoveAll(client *sftp.Client, path string) error {
	info, err := client.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return client.Remove(path)
	}
	// Walk collects all entries; remove in reverse order so children come before parents.
	var paths []string
	w := client.Walk(path)
	for w.Step() {
		if w.Err() != nil {
			continue
		}
		paths = append(paths, w.Path())
	}
	for i := len(paths) - 1; i >= 0; i-- {
		_ = client.Remove(paths[i])
	}
	return nil
}

// progressReader wraps an io.Reader to emit TransferProgress updates on each Read.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	ch    chan<- ports.TransferProgress
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.read += int64(n)
	var pct int
	if pr.total > 0 {
		pct = int(pr.read * 100 / pr.total)
	}
	select {
	case pr.ch <- ports.TransferProgress{Percent: pct, Bytes: pr.read, Total: pr.total}:
	default:
	}
	return n, err
}

// ---- Remote file operations (all via SFTP) ----

// ListRemoteFiles lists the contents of path on the remote server.
func (s *serverService) ListRemoteFiles(alias, path string) ([]domain.FileEntry, error) {
	if path == "" {
		path = "."
	}
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		return nil, fmt.Errorf("ls remote failed: %w", err)
	}
	defer cleanup()

	infos, err := client.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("ls remote failed: %w", err)
	}

	var dirs, files []domain.FileEntry
	for _, info := range infos {
		e := domain.FileEntry{
			Name:    info.Name(),
			IsDir:   info.IsDir(),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
		}
		if info.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return append(dirs, files...), nil
}

// MkdirRemote creates a directory (and any missing parents) on the remote server.
func (s *serverService) MkdirRemote(alias, path string) error {
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		return fmt.Errorf("mkdir remote failed: %w", err)
	}
	defer cleanup()
	if err := client.MkdirAll(path); err != nil {
		return fmt.Errorf("mkdir remote failed: %w", err)
	}
	return nil
}

// DeleteRemote removes a file or directory tree on the remote server.
func (s *serverService) DeleteRemote(alias, path string) error {
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		return fmt.Errorf("delete remote failed: %w", err)
	}
	defer cleanup()
	if err := sftpRemoveAll(client, path); err != nil {
		return fmt.Errorf("delete remote failed: %w", err)
	}
	return nil
}

// RenameRemote renames/moves a file on the remote server.
func (s *serverService) RenameRemote(alias, oldPath, newPath string) error {
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		return fmt.Errorf("rename remote failed: %w", err)
	}
	defer cleanup()
	if err := client.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename remote failed: %w", err)
	}
	return nil
}

// UploadFile copies a local file to the remote server via SFTP.
// Progress updates (0–100%) are sent to the progress channel when non-nil.
func (s *serverService) UploadFile(alias, localPath, remotePath string, progress chan<- ports.TransferProgress) error {
	if progress != nil {
		progress <- ports.TransferProgress{Percent: 0}
	}
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: err}
		}
		return err
	}
	defer cleanup()

	localFile, err := os.Open(localPath)
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: err}
		}
		return fmt.Errorf("upload failed: %w", err)
	}
	defer localFile.Close()

	info, err := localFile.Stat()
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: err}
		}
		return fmt.Errorf("upload failed: %w", err)
	}
	total := info.Size()

	remoteFile, err := client.Create(remotePath)
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: fmt.Errorf("upload failed: %w", err)}
		}
		return fmt.Errorf("upload failed: %w", err)
	}
	defer remoteFile.Close()

	var src io.Reader = localFile
	if progress != nil {
		src = &progressReader{r: localFile, total: total, ch: progress}
	}
	if _, err := io.Copy(remoteFile, src); err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: fmt.Errorf("upload failed: %w", err)}
		}
		return fmt.Errorf("upload failed: %w", err)
	}
	if progress != nil {
		progress <- ports.TransferProgress{Percent: 100, Bytes: total, Total: total, Done: true}
	}
	return nil
}

// DownloadFile copies a remote file to the local filesystem via SFTP.
// Progress updates (0–100%) are sent to the progress channel when non-nil.
func (s *serverService) DownloadFile(alias, remotePath, localPath string, progress chan<- ports.TransferProgress) error {
	if progress != nil {
		progress <- ports.TransferProgress{Percent: 0}
	}
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: err}
		}
		return err
	}
	defer cleanup()

	remoteFile, err := client.Open(remotePath)
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: fmt.Errorf("download failed: %w", err)}
		}
		return fmt.Errorf("download failed: %w", err)
	}
	defer remoteFile.Close()

	info, err := remoteFile.Stat()
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: fmt.Errorf("download failed: %w", err)}
		}
		return fmt.Errorf("download stat failed: %w", err)
	}
	total := info.Size()

	localFile, err := os.Create(localPath)
	if err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: fmt.Errorf("download failed: %w", err)}
		}
		return fmt.Errorf("download failed: %w", err)
	}
	defer localFile.Close()

	var src io.Reader = remoteFile
	if progress != nil {
		src = &progressReader{r: remoteFile, total: total, ch: progress}
	}
	if _, err := io.Copy(localFile, src); err != nil {
		if progress != nil {
			progress <- ports.TransferProgress{Err: fmt.Errorf("download failed: %w", err)}
		}
		return fmt.Errorf("download failed: %w", err)
	}
	if progress != nil {
		progress <- ports.TransferProgress{Percent: 100, Bytes: total, Total: total, Done: true}
	}
	return nil
}

// ChmodRemote changes the permission bits of a remote path.
func (s *serverService) ChmodRemote(alias, path string, mode uint32) error {
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		return fmt.Errorf("chmod remote failed: %w", err)
	}
	defer cleanup()
	if err := client.Chmod(path, os.FileMode(mode)); err != nil {
		return fmt.Errorf("chmod remote failed: %w", err)
	}
	return nil
}

// HomeDirRemote resolves the login user's home directory, used as the initial
// location of the remote pane (previously it started at ".", which is whatever
// directory sshd happened to pick).
func (s *serverService) HomeDirRemote(alias string) (string, error) {
	client, cleanup, err := openSFTP(alias)
	if err != nil {
		return "", fmt.Errorf("home dir failed: %w", err)
	}
	defer cleanup()
	wd, err := client.Getwd()
	if err != nil {
		return "", fmt.Errorf("home dir failed: %w", err)
	}
	return wd, nil
}
