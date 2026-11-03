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

package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New constructs a Sugared Logger that writes to a file and
// provides human-readable timestamps.
//
// Logging is best-effort: a read-only or missing HOME must never stop the TUI
// from starting, so the function falls back to a temp-dir log and finally to a
// no-op logger instead of returning an error.
func New(service string, outputPaths ...string) (*zap.SugaredLogger, error) {
	config := zap.NewProductionConfig()

	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)

	config.DisableStacktrace = true
	config.InitialFields = map[string]any{
		"service": service,
	}

	if len(outputPaths) > 0 {
		config.OutputPaths = outputPaths
	} else if path := defaultLogPath(); path != "" {
		config.OutputPaths = []string{path}
	} else {
		return zap.NewNop().Sugar(), nil
	}

	log, err := config.Build(zap.WithCaller(true))
	if err != nil {
		return nil, err
	}

	return log.Sugar(), nil
}

// defaultLogPath picks a writable log file, or "" when none is available.
func defaultLogPath() string {
	candidates := make([]string, 0, 2)
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".lazyssh"))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), "lazyssh-"+itoa(os.Getuid())))

	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			continue
		}
		// Probe writability: MkdirAll succeeds on an existing read-only dir.
		probe, err := os.CreateTemp(dir, ".probe-*")
		if err != nil {
			continue
		}
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		return filepath.Join(dir, "lazyssh.log")
	}
	return ""
}

// itoa is a tiny int-to-string helper that avoids pulling strconv into the hot
// import list for one call.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
