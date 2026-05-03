package devloop

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/andrioid/gastro/internal/watcher"
)

// warnEmptySources emits a non-fatal warning when pages/ or components/
// exists but contains no .gastro files (or doesn't exist at all). The
// poller picks up newly-added files automatically, so the loop continues
// — this is purely a UX hint for users who started gastro before
// scaffolding any pages and would otherwise wonder why nothing happens.
//
// Output goes to the supplied writer (Run uses os.Stderr) so tests can
// capture it without juggling os.Stderr globals.
//
// root is the project root. An empty root resolves to ".", matching the
// rest of the watcher's path handling.
func warnEmptySources(w io.Writer, root string) {
	if root == "" {
		root = "."
	}
	for _, dir := range []string{"pages", "components"} {
		full := filepath.Join(root, dir)
		if !hasGastroFiles(full) {
			fmt.Fprintf(w,
				"gastro: %s/ has no .gastro files yet — watching anyway, will pick up new ones\n",
				dir)
		}
	}

	// Mirror `gastro dev`'s historical "watching <root>" log line for
	// continuity with the old console output. Skip when root is "."
	// because the bare "watching ." line carries no information.
	if root != "." {
		abs, err := filepath.Abs(root)
		if err == nil {
			fmt.Fprintf(w, "gastro: watching %s\n", abs)
		}
	}
}

// stderrSink is the production writer for warnEmptySources. Pulled out
// as a package-level variable solely so a test can swap it; production
// code keeps using os.Stderr.
var stderrSink io.Writer = os.Stderr

// hasGastroFiles reports whether dir exists and contains at least one
// .gastro file (recursively). Errors during walk count as "no files" —
// the poller will surface real I/O problems as they happen.
func hasGastroFiles(dir string) bool {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	files, err := watcher.CollectGastroFiles(dir)
	if err != nil {
		return false
	}
	return len(files) > 0
}
