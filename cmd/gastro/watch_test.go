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

// TestDev_AcceptsWatchFlag: --watch is the sole allowed flag for `gastro
// dev`. Verifies the parsed globs are returned in the right order, the
// flag is repeatable, comma-separated values are accepted, and the
// --watch=GLOB form works.
func TestDev_AcceptsWatchFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			"single --watch with separate value",
			[]string{"--watch", "i18n/*.po"},
			[]string{"i18n/*.po"},
		},
		{
			"--watch=VALUE form",
			[]string{"--watch=i18n/*.po"},
			[]string{"i18n/*.po"},
		},
		{
			"repeatable --watch",
			[]string{"--watch", "i18n/*.po", "--watch", "config/*.toml"},
			[]string{"i18n/*.po", "config/*.toml"},
		},
		{
			"comma-separated values",
			[]string{"--watch", "i18n/*.po,config/*.toml"},
			[]string{"i18n/*.po", "config/*.toml"},
		},
		{
			"dedups identical globs",
			[]string{"--watch", "i18n/*.po", "--watch", "i18n/*.po"},
			[]string{"i18n/*.po"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDevWatchFlags(tc.args)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !equalStringSlices(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDev_WatchMissingValue: --watch with no value reports a clear
// error instead of silently swallowing the next arg.
func TestDev_WatchMissingValue(t *testing.T) {
	_, err := parseDevWatchFlags([]string{"--watch"})
	if err == nil {
		t.Fatal("expected error for --watch with no value")
	}
	if !strings.Contains(err.Error(), "--watch") || !strings.Contains(err.Error(), "value") {
		t.Errorf("expected helpful error mentioning --watch and value; got %q", err.Error())
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

	t.Run("--watch-root with value", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--watch-root", "/tmp/x"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fl.WatchRoot != "/tmp/x" {
			t.Errorf("WatchRoot = %q", fl.WatchRoot)
		}
	})

	t.Run("--watch-root=value form", func(t *testing.T) {
		fl, err := parseWatchArgs([]string{"--watch-root=../mod"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if fl.WatchRoot != "../mod" {
			t.Errorf("WatchRoot = %q", fl.WatchRoot)
		}
	})

	t.Run("repeated --watch-root rejected", func(t *testing.T) {
		_, err := parseWatchArgs([]string{"--watch-root", "a", "--watch-root", "b"})
		if err == nil || !strings.Contains(err.Error(), "only be specified once") {
			t.Errorf("expected single-use error, got %v", err)
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
		cmd     string
		flagged bool
		hint    string
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

// TestResolveGoWatchRoot exercises the three branches of the resolver
// the production runWatch funnels through: explicit override, walk-up
// success, and walk-up fallback. The walk-up tests rely on chdir into
// a sandbox layout so they don't accidentally pick up the repo's own
// go.mod by walking past the test root.
func TestResolveGoWatchRoot(t *testing.T) {
	t.Run("explicit --watch-root resolves to abs path", func(t *testing.T) {
		dir := t.TempDir()
		got, source, err := resolveGoWatchRoot(dir)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if source != "--watch-root" {
			t.Errorf("source = %q, want --watch-root", source)
		}
		if got != dir {
			t.Errorf("path = %q, want %q", got, dir)
		}
	})

	t.Run("explicit --watch-root rejects non-directory", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "not-a-dir")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, _, err := resolveGoWatchRoot(file)
		if err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("expected not-a-directory error, got %v", err)
		}
	})

	t.Run("walk-up finds go.mod above cwd", func(t *testing.T) {
		// Sandbox under repo's tmp/ so walk-up doesn't bleed into the
		// real go.mod. .git at the sandbox root caps the walk.
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		base := filepath.Join(repoRoot, "tmp", "test-projects")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		sandbox, err := os.MkdirTemp(base, "resolve-*")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(sandbox) })
		mustMkdirT(t, filepath.Join(sandbox, ".git"))

		mod := filepath.Join(sandbox, "app")
		mustMkdirT(t, mod)
		mustWriteT(t, filepath.Join(mod, "go.mod"), "module x\n")

		web := filepath.Join(mod, "internal", "web")
		mustMkdirT(t, web)

		chdirT(t, web)
		got, source, err := resolveGoWatchRoot("")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if source != "go.mod" {
			t.Errorf("source = %q, want go.mod", source)
		}
		// Resolve symlinks for comparison — macOS prefixes /private/var
		// for things like /var/folders, so getwd vs MkdirTemp can
		// disagree even though they point at the same inode.
		wantAbs, _ := filepath.EvalSymlinks(mod)
		gotAbs, _ := filepath.EvalSymlinks(got)
		if gotAbs != wantAbs {
			t.Errorf("path = %q, want %q", gotAbs, wantAbs)
		}
	})

	t.Run("no go.mod falls back to cwd", func(t *testing.T) {
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		base := filepath.Join(repoRoot, "tmp", "test-projects")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		sandbox, err := os.MkdirTemp(base, "resolve-nomod-*")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(sandbox) })
		// .git at sandbox root caps the walk before it can find the
		// repo's own go.mod above tmp/.
		mustMkdirT(t, filepath.Join(sandbox, ".git"))

		web := filepath.Join(sandbox, "internal", "web")
		mustMkdirT(t, web)

		chdirT(t, web)
		got, source, err := resolveGoWatchRoot("")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if source != "no go.mod found" {
			t.Errorf("source = %q, want %q", source, "no go.mod found")
		}
		wantAbs, _ := filepath.EvalSymlinks(web)
		gotAbs, _ := filepath.EvalSymlinks(got)
		if gotAbs != wantAbs {
			t.Errorf("path = %q, want %q (cwd)", gotAbs, wantAbs)
		}
	})
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
