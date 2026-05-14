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
	cfg              Config
	root             string
	goRoot           string
	extDeps          *watcher.ExternalDeps
	escalate         func(watcher.ChangeType)
	debounced        func()
	modTimes         map[string]time.Time
	fileContents     map[string]string
	embedCache       []string
	embedDepsVersion uint64
	// goExcludeBasenames is matched against directory basenames anywhere
	// in the goRoot tree (always-on defaults like vendor/, node_modules/).
	goExcludeBasenames []string
	// goExcludePrefixes is matched as a slash-normalised prefix relative
	// to goRoot (user-supplied via --exclude).
	goExcludePrefixes []string
	// extraWatchGlobs is the cfg.ExtraWatch list copied at seed time.
	// Each pattern is evaluated relative to root via filepath.Glob on
	// every poll; matches participate in modTimes / currentFiles like
	// the built-in surfaces.
	extraWatchGlobs []string
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
	goRoot := cfg.GoWatchRoot
	if goRoot == "" {
		goRoot = root
	}
	s := &watcherState{
		cfg:          cfg,
		root:         root,
		goRoot:       goRoot,
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
	s.seedEmbed()
	if cfg.WatchGoFiles {
		s.goExcludeBasenames = append(s.goExcludeBasenames, defaultGoExcludeBasenames...)
		for _, ex := range cfg.ExtraExcludes {
			s.goExcludePrefixes = append(s.goExcludePrefixes, normaliseExclude(ex))
		}
		seedGoFiles(s.goRoot, s.goExcludeBasenames, s.goExcludePrefixes, s.modTimes)
	}
	s.extraWatchGlobs = append([]string(nil), cfg.ExtraWatch...)
	for _, f := range collectExtraWatchFiles(root, s.extraWatchGlobs) {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		s.modTimes[f] = info.ModTime()
	}
	return s
}

// collectExtraWatchFiles expands each glob (rooted at root) and returns
// the matched filesystem entries. Missing matches return an empty slice;
// directories among matches are skipped so callers don't add directories
// to modTimes. Errors during glob are silently ignored — the next tick
// will retry.
func collectExtraWatchFiles(root string, globs []string) []string {
	if len(globs) == 0 {
		return nil
	}
	var out []string
	for _, g := range globs {
		pattern := g
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(root, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || info.IsDir() {
				continue
			}
			out = append(out, m)
		}
	}
	return out
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

func (s *watcherState) syncEmbedCache() {
	paths, ver := s.extDeps.Snapshot()
	if ver == s.embedDepsVersion && s.embedCache != nil {
		return
	}
	newSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		newSet[p] = struct{}{}
	}
	for _, old := range s.embedCache {
		if _, ok := newSet[old]; !ok {
			delete(s.modTimes, old)
		}
	}
	s.embedCache = paths
	s.embedDepsVersion = ver
}

func (s *watcherState) seedEmbed() {
	s.syncEmbedCache()
	for _, f := range s.embedCache {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		s.modTimes[f] = info.ModTime()
	}
}

// runLoop polls the project tree at PollInterval, classifies each
// change, and feeds the escalate/debounced pair owned by Run. The state
// (modTimes, fileContents, embedCache, embedDepsVersion) is
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
	_ = s.extDeps // accessed via s.syncEmbedCache()
	escalate := s.escalate
	debounced := s.debounced
	goRoot := s.goRoot
	goExcludeBasenames := s.goExcludeBasenames
	goExcludePrefixes := s.goExcludePrefixes

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

		// Embed deps — driven by extDeps, refreshed only when its
		// version counter changes.
		s.syncEmbedCache()
		for _, f := range s.embedCache {
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
				logf("gastro: %s changed (embed)\n", f)
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
			goFiles := collectGoFiles(goRoot, goExcludeBasenames, goExcludePrefixes)
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

		// Extra --watch globs (e.g. "i18n/*.po"). Any change/create on a
		// matched file escalates to ChangeRestart — these patterns are
		// intended for files baked into the binary via //go:embed, where
		// the in-memory state is stale until rebuild.
		if len(s.extraWatchGlobs) > 0 {
			for _, f := range collectExtraWatchFiles(s.root, s.extraWatchGlobs) {
				currentFiles[f] = true
				info, err := os.Stat(f)
				if err != nil {
					continue
				}

				prev, known := modTimes[f]
				if !known {
					logf("gastro: new file %s (watch)\n", f)
					modTimes[f] = info.ModTime()
					escalate(watcher.ChangeRestart)
					changed = true
					continue
				}

				if info.ModTime().After(prev) {
					logf("gastro: %s changed (watch)\n", f)
					modTimes[f] = info.ModTime()
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
func seedGoFiles(root string, basenames, prefixes []string, modTimes map[string]time.Time) {
	for _, f := range collectGoFiles(root, basenames, prefixes) {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		modTimes[f] = info.ModTime()
	}
}

// collectGoFiles walks root and returns *.go files not blocked by either
// exclude set:
//
//   - basenames: matched against the bare directory or file name (e.g.
//     "vendor", ".gastro"). Skipped wherever they appear in the tree;
//     this is what makes nested node_modules/ inside a monorepo also
//     get pruned, not just one at the root.
//
//   - prefixes: slash-normalised path prefixes relative to root (e.g.
//     "custom/deep/"). Matched the same way the historical exclude list
//     was, so existing user --exclude entries keep their semantics.
//
// Walks return forward-slash paths via filepath.ToSlash so the prefix
// match is portable. Errors during walk are silently skipped — the
// watcher recovers on the next tick. This matches the existing static/
// handling.
func collectGoFiles(root string, basenames, prefixes []string) []string {
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

		// Basename excludes apply anywhere in the tree.
		base := filepath.Base(path)
		for _, b := range basenames {
			if base == b {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Prefix excludes apply relative to root.
		for _, ex := range prefixes {
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
