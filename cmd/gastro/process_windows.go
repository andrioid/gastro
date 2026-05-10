//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/google/shlex"
)

// Windows variant of the App lifecycle wrapper. See process_unix.go for
// the design notes — the public API (Start / Stop / Wait) is identical,
// but the kill semantics are different.
//
// On Unix we put the child in its own process group with Setpgid and
// kill the whole group with negative-pid signaling. Windows has no
// process groups for signaling purposes; the closest cross-runtime
// equivalent is "kill the process tree", which we implement by
// shelling out to `taskkill /T /F /PID <pid>` (Windows-supplied,
// available since XP/2003 on every supported runner).
//
// We also set the CREATE_NEW_PROCESS_GROUP creation flag so that a
// future caller-driven Ctrl-Break delivery is possible if we ever
// outgrow the taskkill approach. Today only Stop uses it; Start just
// inherits stdout/stderr like the Unix variant.
type App struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stopped bool
}

// gracePeriod is how long Stop waits between the soft kill attempt
// (taskkill without /F) and the hard kill (taskkill /F). Matches the
// Unix variant's value so user-visible shutdown timings stay portable.
const gracePeriod = 5 * time.Second

// Start spawns command in its own process group on Windows. argv is
// parsed with shlex for parity with the Unix variant.
func Start(ctx context.Context, command string, env []string) (*App, error) {
	argv, err := shlex.Split(command)
	if err != nil {
		return nil, fmt.Errorf("parse command: %w", err)
	}
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &App{cmd: cmd}, nil
}

// Stop attempts a graceful taskkill, waits up to gracePeriod, then
// escalates to taskkill /F if the process is still alive. Idempotent
// and safe on a nil receiver, matching the Unix variant.
//
// `taskkill /T <pid>` walks the process tree (children, grandchildren,
// …) and asks each process to exit, the closest Windows equivalent of
// the Unix `kill(-pgid, SIGTERM)` we use elsewhere. The /F flag adds a
// non-cooperative force-terminate when the soft kill expires.
func (a *App) Stop() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return nil
	}
	a.stopped = true

	if a.cmd == nil || a.cmd.Process == nil {
		return nil
	}

	pid := a.cmd.Process.Pid
	_ = exec.Command("taskkill", "/T", "/PID", fmt.Sprint(pid)).Run()

	done := make(chan error, 1)
	go func() { done <- a.cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil && !isExitError(err) {
			return err
		}
		return nil
	case <-time.After(gracePeriod):
		_ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(pid)).Run()
		<-done
		return fmt.Errorf("process tree rooted at pid %d did not exit within %s of taskkill; sent /F",
			pid, gracePeriod)
	}
}

// Wait blocks until the process exits and returns its exit error (if
// any). Safe on a nil receiver.
func (a *App) Wait() error {
	if a == nil || a.cmd == nil {
		return nil
	}
	return a.cmd.Wait()
}

// isExitError reports whether err is a non-zero exit status, treated
// as an expected outcome of forced termination rather than a real
// failure.
func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}
