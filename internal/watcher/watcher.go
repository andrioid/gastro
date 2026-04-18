package watcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ChangeType describes what kind of action a file change requires.
type ChangeType int

const (
	ChangeReload  ChangeType = iota // Template/asset changed — reload from disk
	ChangeRestart                   // Frontmatter changed — recompile and restart
)

// Section describes which part of a .gastro file changed.
type Section int

const (
	SectionUnknown     Section = iota
	SectionFrontmatter         // Go code between --- delimiters
	SectionTemplate            // HTML template body
)

// ClassifyChange determines whether a file change needs a reload or restart.
func ClassifyChange(file string, section Section) ChangeType {
	// Static assets only need reload
	if strings.HasPrefix(file, "static/") {
		return ChangeReload
	}

	// Template-only changes can reload from disk
	if section == SectionTemplate {
		return ChangeReload
	}

	// Frontmatter changes need recompile + restart
	return ChangeRestart
}

// CollectGastroFiles walks a directory and returns all .gastro file paths.
func CollectGastroFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".gastro") {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}

// ExternalDeps holds the set of markdown files referenced by
// {{ markdown "..." }} directives across all compiled .gastro files. The
// dev watcher polls these paths for changes; the compiler populates them
// from CompileResult.MarkdownDeps after each successful compile.
//
// Paths are canonicalized via filepath.EvalSymlinks where possible so the
// same file reached through a symlink and its target is tracked only once.
// If EvalSymlinks fails (e.g. broken link, transient fs error), the input
// path is used as-is.
//
// ExternalDeps is safe for concurrent Set/Snapshot calls.
type ExternalDeps struct {
	mu      sync.Mutex
	paths   []string // canonicalized, sorted, deduped
	version uint64
}

// Set replaces the tracked dependency list. The version counter is bumped
// only if the canonicalized, deduped list differs from the previous state,
// so consumers can cheaply detect real changes. Callers should invoke Set
// only after a successful compile; on compile failure, skip the call to
// retain the last-known-good dep list.
func (e *ExternalDeps) Set(paths []string) {
	canon := canonicalizePaths(paths)

	e.mu.Lock()
	defer e.mu.Unlock()
	if stringSlicesEqual(e.paths, canon) {
		return
	}
	e.paths = canon
	e.version++
}

// Snapshot returns a copy of the current dep list and the version counter.
// Consumers can compare the returned version against a previously observed
// one to skip redundant work when nothing has changed.
func (e *ExternalDeps) Snapshot() (paths []string, version uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.paths))
	copy(out, e.paths)
	return out, e.version
}

// canonicalizePaths resolves symlinks where possible, deduplicates, and
// returns a sorted slice.
func canonicalizePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		cp := canonicalizePath(p)
		if _, ok := seen[cp]; ok {
			continue
		}
		seen[cp] = struct{}{}
		out = append(out, cp)
	}
	sort.Strings(out)
	return out
}

// canonicalizePath returns filepath.EvalSymlinks(p) when it succeeds,
// otherwise the input path. This keeps the function total even when the
// target file is missing or the symlink is broken.
func canonicalizePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

func stringSlicesEqual(a, b []string) bool {
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

// CollectAllFiles walks a directory and returns all file paths.
// Used for watching static asset directories where any file type is relevant.
func CollectAllFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}

// DetectChangedSection compares old and new file content and determines which
// section changed. If the frontmatter changed, returns SectionFrontmatter
// (which takes priority since it requires a restart).
func DetectChangedSection(oldContent, newContent string) Section {
	oldFM, oldBody := splitQuick(oldContent)
	newFM, newBody := splitQuick(newContent)

	if oldFM != newFM {
		return SectionFrontmatter
	}

	if oldBody != newBody {
		return SectionTemplate
	}

	return SectionUnknown
}

// splitQuick does a fast split of content at --- delimiters without full parsing.
func splitQuick(content string) (frontmatter, body string) {
	lines := strings.SplitN(content, "\n", -1)
	firstDelim := -1
	secondDelim := -1

	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if firstDelim == -1 {
				firstDelim = i
			} else {
				secondDelim = i
				break
			}
		}
	}

	if firstDelim == -1 || secondDelim == -1 {
		return "", content
	}

	fm := strings.Join(lines[firstDelim+1:secondDelim], "\n")
	bd := ""
	if secondDelim+1 < len(lines) {
		bd = strings.Join(lines[secondDelim+1:], "\n")
	}

	return fm, bd
}

// Debounce returns a function that delays invoking fn until after duration has
// elapsed since the last time the returned function was called.
func Debounce(duration time.Duration, fn func()) func() {
	var timer *time.Timer
	var mu sync.Mutex

	return func() {
		mu.Lock()
		defer mu.Unlock()

		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(duration, fn)
	}
}
