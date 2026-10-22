// Package setup is `relay setup`, `relay add` and `relay doctor`: the
// commands that prepare a Mac once, connect a repository, and say what is
// wrong. Everything that touches the machine goes through Sys so that tests
// can replace the commands and the home directory.
package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Exec runs a command and returns its stdout, stderr and exit code.
type Exec func(ctx context.Context, name string, args ...string) (stdout, stderr string, exit int, err error)

// Sys is the machine as the setup commands see it.
type Sys struct {
	Exec Exec
	// Home is the user's home directory; paths below are relative to it.
	Home string
	// Env reads an environment variable.
	Env func(string) string
	// Arch is "arm64" or "amd64".
	Arch string
	// Out receives progress lines.
	Out io.Writer
	// Version is the relay binary's version, for the plugin skew check.
	Version string
}

// Default is the real machine.
func Default() *Sys {
	home, _ := os.UserHomeDir()
	return &Sys{Exec: RealExec, Home: home, Env: os.Getenv, Arch: runtime.GOARCH, Out: os.Stderr}
}

// RealExec runs the command with the current environment.
func RealExec(ctx context.Context, name string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	exit := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
		err = nil
	}
	return out.String(), errb.String(), exit, err
}

// run executes and turns a non-zero exit into an error carrying stderr.
func (s *Sys) run(ctx context.Context, name string, args ...string) (string, error) {
	out, stderr, exit, err := s.Exec(ctx, name, args...)
	if err != nil {
		return out, fmt.Errorf("%s: %w", name, err)
	}
	if exit != 0 {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(out)
		}
		return out, fmt.Errorf("%s %s: exit %d: %s", name, firstArg(args), exit, lastLine(msg))
	}
	return out, nil
}

func (s *Sys) logf(format string, args ...any) {
	if s.Out != nil {
		fmt.Fprintf(s.Out, format+"\n", args...)
	}
}

// Paths under the home directory.
func (s *Sys) relayDir() string          { return filepath.Join(s.Home, ".relay") }
func (s *Sys) distDir() string           { return filepath.Join(s.relayDir(), "runner-dist") }
func (s *Sys) runnersDir() string        { return filepath.Join(s.relayDir(), "runners") }
func (s *Sys) launchAgents() string      { return filepath.Join(s.Home, "Library", "LaunchAgents") }
func (s *Sys) logsDir() string           { return filepath.Join(s.Home, "Library", "Logs", "relay") }
func (s *Sys) botPlist() string          { return filepath.Join(s.launchAgents(), BotLabel+".plist") }
func (s *Sys) configPath() string        { return filepath.Join(s.Home, ".config", "relay", "config.yml") }
func (s *Sys) runnerDir(r string) string { return filepath.Join(s.runnersDir(), r) }

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return s
}
