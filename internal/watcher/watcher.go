package watcher

import (
	"os"
	"path/filepath"
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

// CollectMarkdownFiles walks a directory and returns all .md file paths,
// skipping hidden directories (names starting with '.') and common
// non-source directories like node_modules. Used for watching markdown
// content referenced by {{ markdown "..." }} directives.
func CollectMarkdownFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if path != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "tmp") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})

	return files, err
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
