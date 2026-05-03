package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// chdirMu serialises chdir-based tests in this package.
var watchChdirMu sync.Mutex

// chdirT chdirs into dir for the duration of the test, restoring on cleanup.
func chdirT(t *testing.T, dir string) {
	t.Helper()
	watchChdirMu.Lock()
	orig, err := os.Getwd()
	if err != nil {
		watchChdirMu.Unlock()
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		watchChdirMu.Unlock()
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
		watchChdirMu.Unlock()
	})
}

// TestDev_RejectsUnknownFlags asserts the canonical Q5 wording is returned
// verbatim for any flag passed to `gastro dev`. Production code uses the
// same devFlagRejectionMessage helper so the test compares against the
// same constant rather than a hardcoded string copy.
func TestDev_RejectsUnknownFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"long --build", []string{"--build", "x"}},
		{"long --run", []string{"--run", "go run ."}},
		{"short -b", []string{"-b", "x"}},
		{"unknown -x", []string{"-x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDevArgs(tc.args)
			if err == nil {
				t.Fatalf("expected error for args %v", tc.args)
			}
			expected := devFlagRejectionMessage(tc.args[0])
			if err.Error() != expected {
				t.Errorf("error did not match canonical message.\ngot:  %q\nwant: %q",
					err.Error(), expected)
			}
		})
	}
}

func TestDev_AcceptsNoArgs(t *testing.T) {
	if err := validateDevArgs(nil); err != nil {
		t.Errorf("expected nil for empty args, got %v", err)
	}
	if err := validateDevArgs([]string{}); err != nil {
		t.Errorf("expected nil for [], got %v", err)
	}
}

// TestParseWatchArgs covers the flag surface defined in the plan (\u00a74).
// Repeatable flags, value/equals forms, missing-value errors, unknown
// flags, and --help all land here so a regression in any of them
// surfaces independently of the watch loop's runtime behaviour.
func TestParseWatchArgs(t *testing.T) {
	t.Run("requires --run", func(t *testing.T) {
		fl, err := parseWatchArgs(nil)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fl.Run != "" {
			t.Errorf("expected empty Run, got %q", fl.Run)
		}
	})

	t.Run("--run with value", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--run", "go run ."})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fl.Run != "go run ." {
			t.Errorf("Run = %q", fl.Run)
		}
	})

	t.Run("--run=value form", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--run=tmp/app"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fl.Run != "tmp/app" {
			t.Errorf("Run = %q", fl.Run)
		}
	})

	t.Run("-r short form", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"-r", "x"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fl.Run != "x" {
			t.Errorf("Run = %q", fl.Run)
		}
	})

	t.Run("repeated --run rejected", func(t *testing.T) {
		_, err := parseWatchArgs([]string{"--run", "a", "--run", "b"})
		if err == nil || !strings.Contains(err.Error(), "only be specified once") {
			t.Errorf("expected single-use error, got %v", err)
		}
	})

	t.Run("repeated --build accumulates", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--build", "a", "--build", "b", "--build", "c"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(fl.Build) != 3 || fl.Build[0] != "a" || fl.Build[2] != "c" {
			t.Errorf("Build = %v", fl.Build)
		}
	})

	t.Run("repeated --exclude accumulates", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--exclude", "x", "--exclude", "y"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(fl.Excludes) != 2 {
			t.Errorf("Excludes = %v", fl.Excludes)
		}
	})

	t.Run("--debounce parses duration", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--debounce", "150ms"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fl.Debounce != 150*time.Millisecond {
			t.Errorf("Debounce = %v", fl.Debounce)
		}
	})

	t.Run("--debounce rejects garbage", func(t *testing.T) {
		_, err := parseWatchArgs([]string{"--debounce", "not-a-duration"})
		if err == nil {
			t.Fatal("expected parse error")
		}
	})

	t.Run("missing value reports flag name", func(t *testing.T) {
		_, err := parseWatchArgs([]string{"--build"})
		if err == nil || !strings.Contains(err.Error(), "--build needs a value") {
			t.Errorf("expected --build needs a value, got %v", err)
		}
	})

	t.Run("unknown flag reports name", func(t *testing.T) {
		_, err := parseWatchArgs([]string{"--bogus"})
		if err == nil || !strings.Contains(err.Error(), "unknown flag --bogus") {
			t.Errorf("expected unknown flag error, got %v", err)
		}
	})

	t.Run("--help signals help", func(t *testing.T) {
		_, err := parseWatchArgs([]string{"--help"})
		if err != errHelp {
			t.Errorf("expected errHelp sentinel, got %v", err)
		}
	})

	t.Run("--quiet sets flag", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--quiet"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !fl.Quiet {
			t.Error("Quiet not set")
		}
	})

	t.Run("ldflags-style quoted args via shlex", func(t *testing.T) {
		// This is the canonical hard case: a --build value containing
		// inner single-quoted whitespace. parseWatchArgs treats the
		// argv element as opaque (it's the user's responsibility to
		// quote it correctly in their shell); shlex handles the inner
		// split when runShellCommand parses it later. So at parse time
		// the string is preserved intact.
		fl, err := parseWatchArgs([]string{"--build", `go build -ldflags '-X main.version=1.0' ./...`})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !strings.Contains(fl.Build[0], "-X main.version=1.0") {
			t.Errorf("ldflags lost: %q", fl.Build[0])
		}
		// And shlex must round-trip it correctly.
		argv, err := shlexSplit(fl.Build[0])
		if err != nil {
			t.Fatalf("shlex: %v", err)
		}
		want := []string{"go", "build", "-ldflags", "-X main.version=1.0", "./..."}
		if len(argv) != len(want) {
			t.Fatalf("shlex argv = %v, want %v", argv, want)
		}
		for i := range want {
			if argv[i] != want[i] {
				t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
			}
		}
	})
}

// TestSuspectedBuildOutputCollision exercises the heads-up warning
// heuristic. False positives are non-fatal so the test only asserts the
// "obvious bad" cases produce a non-empty result and the "obvious good"
// cases don't.
func TestSuspectedBuildOutputCollision(t *testing.T) {
	cases := []struct {
		cmd      string
		flagged  bool
		hint     string
	}{
		{"go build -o tmp/app ./cmd/myapp", false, ""},
		{"go build -o ./tmp/app ./cmd/myapp", false, ""},
		{"go build -o pages/app ./cmd/myapp", true, "pages/"},
		{"go build -o static/build/app ./cmd/myapp", true, "static/"},
		{"go build -o ./components/foo.bin ./cmd/myapp", true, "components/"},
		{"go build ./cmd/myapp", false, ""}, // no -o
		{"tailwindcss -i in.css -o out.css", false, ""},
	}
	for _, tc := range cases {
		got := suspectedBuildOutputCollision(tc.cmd)
		if tc.flagged {
			if got == "" {
				t.Errorf("%q: expected collision flagged with hint %q, got empty", tc.cmd, tc.hint)
			} else if !strings.HasPrefix(got, tc.hint) {
				t.Errorf("%q: expected hint prefix %q, got %q", tc.cmd, tc.hint, got)
			}
		} else if got != "" {
			t.Errorf("%q: expected no collision, got %q", tc.cmd, got)
		}
	}
}

// TestWriteBuildErrorSignal_Atomic asserts the signal file is written
// atomically (write-to-tmp + rename). Verified by checking the file
// exists with the expected content and no .tmp file is left behind.
func TestWriteBuildErrorSignal_Atomic(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)

	if err := writeBuildErrorSignal("boom\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, ".gastro", ".build-error"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "boom\n" {
		t.Errorf("content = %q, want %q", got, "boom\n")
	}

	if _, err := os.Stat(filepath.Join(dir, ".gastro", ".build-error.tmp")); err == nil {
		t.Error("expected no .build-error.tmp file, but it exists")
	}
}
