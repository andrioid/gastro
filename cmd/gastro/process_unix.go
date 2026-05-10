//go:build unix

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

// App wraps a long-running user-supplied command (the --run argument of
// `gastro watch`) so the CLI can stop it cleanly across rebuilds.
//
// The single non-trivial concern here is process groups. A user who
// runs `gastro watch --run 'go run ./cmd/myapp'` spawns `go run`, which
// in turn execs the compiled binary as a grandchild. If we send SIGTERM
// only to `go run`, the grandchild keeps the listening socket bound to
// the port and the next rebuild fails with "address already in use".
//
// To fix this we put the spawned process in its own process group via
// SysProcAttr.Setpgid=true, then signal the entire group on shutdown
// using Kill(-pgid, SIGTERM) so every descendant (including the
// grandchild) gets the signal at once.
//
// This is a Unix-only concern — Windows uses a different API surface
// (taskkill /T to walk the process tree) and lives in
// process_windows.go alongside this file. The SysProcAttr.Setpgid
// field used below doesn't exist on Windows, so this file is gated
// behind //go:build unix.
type App struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	pgid    int
	stopped bool
}

// gracePeriod is how long Stop waits between SIGTERM and SIGKILL.
// Hardcoded to 5 seconds in v1; making this configurable is deferred
// until someone reports a slow-shutdown binary that needs longer.
const gracePeriod = 5 * time.Second

// Start spawns command in its own process group. The command is parsed
// with shlex so a string like `go run ./cmd/myapp -port 8080` becomes
// the argv [\"go\", \"run\", \"./cmd/myapp\", \"-port\", \"8080\"], and
// quoted segments work the way `sh` would handle them.
//
// stdout and stderr are wired straight to the parent process so the
// user's app logs show up in the `gastro watch` console without any
// reformatting.
//
// The supplied context bounds the process's lifetime: cancelling ctx
// triggers exec.CommandContext's default kill behaviour (SIGKILL to
// the leader only). For cooperative shutdown the caller should use
// Stop, which sends SIGTERM to the whole group first.
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// After Start the OS has assigned a pgid equal to the child's pid
	// (because Setpgid+Pgid==0 means "make me my own group leader").
	// Capture it now in case the child exits before we try to signal it.
	pgid := cmd.Process.Pid

	return &App{cmd: cmd, pgid: pgid}, nil
}

// Stop sends SIGTERM to the entire process group, waits up to
// gracePeriod for the child to exit, then escalates to SIGKILL. Safe to
// call on a nil receiver and idempotent across multiple calls (a
// repeated Stop returns immediately).
//
// Returns nil on a clean exit. Errors from kill/wait are returned but
// callers typically ignore them \u2014 we are tearing down regardless.
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

	// Negative pid means "this process group". The kernel routes the
	// signal to every member, including grandchildren that go run
	// would otherwise leak.
	_ = syscall.Kill(-a.pgid, syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- a.cmd.Wait() }()

	select {
	case err := <-done:
		// Wait can return an exit-status error for a SIGTERM-killed
		// process; that's expected, not interesting to the caller.
		if err != nil && !isExitError(err) {
			return err
		}
		return nil
	case <-time.After(gracePeriod):
		// Grace expired; SIGKILL the group and wait for the wait
		// goroutine to drain.
		_ = syscall.Kill(-a.pgid, syscall.SIGKILL)
		<-done
		return fmt.Errorf("process group %d did not exit within %s of SIGTERM; sent SIGKILL",
			a.pgid, gracePeriod)
	}
}

// Wait blocks until the process exits and returns its exit error (if
// any). Useful when the caller wants to know the process has exited
// without forcing it. Safe on a nil receiver (returns nil immediately).
func (a *App) Wait() error {
	if a == nil || a.cmd == nil {
		return nil
	}
	return a.cmd.Wait()
}

// isExitError reports whether err is a non-zero exit status, which we
// treat as an expected signal-kill outcome rather than a real failure.
func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}
