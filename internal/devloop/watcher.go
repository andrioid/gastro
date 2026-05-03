package devloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrioid/gastro/internal/watcher"
)

// watcherState holds the polling watcher's mutable state. Created and
// seeded synchronously by Run before the watcher goroutine spawns, then
// owned by that goroutine for the lifetime of the loop. Splitting state
// from the loop function lets Run guarantee "seed is complete before any
// user-observable post-startup event" — critical for tests that edit
// files immediately after the initial OnRestart returns.
type watcherState struct {
	cfg                 Config
	root                string
	extDeps             *watcher.ExternalDeps
	escalate            func(watcher.ChangeType)
	debounced           func()
	modTimes            map[string]time.Time
	fileContents        map[string]string
	markdownCache       []string
	markdownDepsVersion uint64
	goExcludes          []string
}

// newWatcherState seeds modTimes/fileContents from the on-disk state of
// the project tree so the first poll cycle doesn't fire spurious
// "new file" events for everything. Caller must invoke before spawning
// the watcher goroutine; the seed is not goroutine-safe.
func newWatcherState(
	cfg Config,
	extDeps *watcher.ExternalDeps,
	escalate func(watcher.ChangeType),
	debounced func(),
) *watcherState {
	root := cfg.ProjectRoot
	if root == "" {
		root = "."
	}
	s := &watcherState{
		cfg:          cfg,
		root:         root,
		extDeps:      extDeps,
		escalate:     escalate,
		debounced:    debounced,
		modTimes:     make(map[string]time.Time),
		fileContents: make(map[string]string),
	}
	for _, dir := range []string{"pages", "components"} {
		s.seedFiles(dir, true)
	}
	s.seedFiles("static", false)
	s.seedMarkdown()
	if cfg.WatchGoFiles {
		s.goExcludes = append(s.goExcludes, defaultGoExcludes...)
		for _, ex := range cfg.ExtraExcludes {
			s.goExcludes = append(s.goExcludes, normaliseExclude(ex))
		}
		seedGoFiles(s.root, s.goExcludes, s.modTimes)
	}
	return s
}

func (s *watcherState) seedFiles(dir string, gastroOnly bool) {
	var files []string
	var err error
	if gastroOnly {
		files, err = watcher.CollectGastroFiles(dir)
	} else {
		files, err = watcher.CollectAllFiles(dir)
	}
	if err != nil {
		return
	}
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		s.modTimes[f] = info.ModTime()
		if gastroOnly {
			if content, err := os.ReadFile(f); err == nil {
				s.fileContents[f] = string(content)
			}
		}
	}
}

func (s *watcherState) syncMarkdownCache() {
	paths, ver := s.extDeps.Snapshot()
	if ver == s.markdownDepsVersion && s.markdownCache != nil {
		return
	}
	newSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		newSet[p] = struct{}{}
	}
	for _, old := range s.markdownCache {
		if _, ok := newSet[old]; !ok {
			delete(s.modTimes, old)
		}
	}
	s.markdownCache = paths
	s.markdownDepsVersion = ver
}

func (s *watcherState) seedMarkdown() {
	s.syncMarkdownCache()
	for _, f := range s.markdownCache {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		s.modTimes[f] = info.ModTime()
	}
}

// runLoop polls the project tree at PollInterval, classifies each
// change, and feeds the escalate/debounced pair owned by Run. The state
// (modTimes, fileContents, markdownCache, markdownDepsVersion) is
// goroutine-local — no cross-goroutine sharing beyond the extDeps
// snapshot and the escalate/debounced closures (which Run protects with
// a mutex). Exits when ctx is cancelled.
//
// This is the polling logic previously inlined in cmd/gastro/main.go
// runDev. Behavioural changes from that extraction are:
//
//   - When cfg.WatchGoFiles is true, *.go files anywhere under
//     ProjectRoot (minus the exclude set) are also watched and treated
//     as restart-class. Used by `gastro watch` (R1+R2). When false the
//     watch surface matches `gastro dev` exactly.
//
//   - Per-change logs ("gastro: pages/x.gastro changed (frontmatter)")
//     can be suppressed via cfg.Quiet. Errors and the regen log line
//     stay on. `gastro dev` leaves Quiet=false, so its console output
//     is unchanged.
func (s *watcherState) runLoop(ctx context.Context) {
	cfg := s.cfg
	logf := func(format string, a ...any) {
		if cfg.Quiet {
			return
		}
		fmt.Printf(format, a...)
	}
	modTimes := s.modTimes
	fileContents := s.fileContents
	_ = s.extDeps // accessed via s.syncMarkdownCache()
	escalate := s.escalate
	debounced := s.debounced
	root := s.root
	goExcludes := s.goExcludes

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.PollInterval):
		}

		changed := false
		currentFiles := make(map[string]bool)

		for _, dir := range []string{"pages", "components"} {
			files, err := watcher.CollectGastroFiles(dir)
			if err != nil {
				continue
			}
			for _, f := range files {
				currentFiles[f] = true
				info, err := os.Stat(f)
				if err != nil {
					continue
				}

				prev, known := modTimes[f]
				if !known {
					// New file — needs full restart (new routes).
					logf("gastro: new file %s\n", f)
					content, _ := os.ReadFile(f)
					fileContents[f] = string(content)
					modTimes[f] = info.ModTime()
					escalate(watcher.ChangeRestart)
					changed = true
					continue
				}

				if info.ModTime().After(prev) {
					content, err := os.ReadFile(f)
					if err != nil {
						continue
					}
					newContent := string(content)
					oldContent := fileContents[f]

					section := watcher.DetectChangedSection(oldContent, newContent)
					ct := watcher.ClassifyChange(f, section)

					label := "template"
					if ct == watcher.ChangeRestart {
						label = "frontmatter"
					}
					logf("gastro: %s changed (%s)\n", f, label)

					fileContents[f] = newContent
					modTimes[f] = info.ModTime()
					escalate(ct)
					changed = true
				}
			}
		}

		// Markdown deps — driven by extDeps, refreshed only when its
		// version counter changes.
		s.syncMarkdownCache()
		for _, f := range s.markdownCache {
			currentFiles[f] = true
			info, err := os.Stat(f)
			if err != nil {
				delete(currentFiles, f)
				continue
			}

			prev, known := modTimes[f]
			if !known {
				logf("gastro: new file %s\n", f)
				modTimes[f] = info.ModTime()
				escalate(watcher.ChangeReload)
				changed = true
				continue
			}

			if info.ModTime().After(prev) {
				logf("gastro: %s changed (markdown)\n", f)
				modTimes[f] = info.ModTime()
				escalate(watcher.ChangeReload)
				changed = true
			}
		}

		// static/
		if files, err := watcher.CollectAllFiles("static"); err == nil {
			for _, f := range files {
				currentFiles[f] = true
				info, err := os.Stat(f)
				if err != nil {
					continue
				}

				prev, known := modTimes[f]
				if !known {
					logf("gastro: new file %s\n", f)
					modTimes[f] = info.ModTime()
					escalate(watcher.ChangeReload)
					changed = true
					continue
				}

				if info.ModTime().After(prev) {
					logf("gastro: %s changed (static)\n", f)
					modTimes[f] = info.ModTime()
					escalate(watcher.ClassifyChange(f, watcher.SectionUnknown))
					changed = true
				}
			}
		}

		// *.go (gastro watch only)
		if cfg.WatchGoFiles {
			goFiles := collectGoFiles(root, goExcludes)
			for _, f := range goFiles {
				currentFiles[f] = true
				info, err := os.Stat(f)
				if err != nil {
					continue
				}

				prev, known := modTimes[f]
				if !known {
					logf("gastro: new file %s\n", f)
					modTimes[f] = info.ModTime()
					escalate(watcher.ChangeRestart)
					changed = true
					continue
				}

				if info.ModTime().After(prev) {
					logf("gastro: %s changed (go)\n", f)
					modTimes[f] = info.ModTime()
					// All .go changes are restart-class; smart
					// classification only applies to .gastro files.
					escalate(watcher.ChangeRestart)
					changed = true
				}
			}
		}

		// Detect deletions across the watched surface.
		for f := range modTimes {
			if !currentFiles[f] {
				logf("gastro: %s deleted\n", f)
				delete(modTimes, f)
				delete(fileContents, f)
				escalate(watcher.ChangeRestart)
				changed = true
			}
		}

		if changed {
			debounced()
		}
	}
}

// normaliseExclude trims leading "./", normalises path separators, and
// ensures trailing slash so prefix matching against filepath.Walk output
// is unambiguous ("vendor" should not match "vendoring/foo.go").
func normaliseExclude(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// seedGoFiles populates modTimes with every *.go file under root that
// passes the exclude filter. Called once at startup so the first scan
// doesn't fire restart events for the entire codebase.
func seedGoFiles(root string, excludes []string, modTimes map[string]time.Time) {
	for _, f := range collectGoFiles(root, excludes) {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		modTimes[f] = info.ModTime()
	}
}

// collectGoFiles walks root and returns *.go files not under any exclude
// prefix. Exclude entries are slash-normalised path prefixes
// ("vendor/", ".gastro/"). Walks return forward-slash paths via
// filepath.ToSlash so the prefix match is portable.
//
// Errors during walk are silently skipped — the watcher recovers on the
// next tick. This matches the existing static/ handling.
func collectGoFiles(root string, excludes []string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		// Apply excludes to both directories and files.
		for _, ex := range excludes {
			if rel == strings.TrimSuffix(ex, "/") || strings.HasPrefix(rel+"/", ex) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	return out
}
