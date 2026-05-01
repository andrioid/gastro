package gastro

import (
	"io/fs"
	"os"
	"path/filepath"
)

// IsDev reports whether the application is running in development mode.
// The gastro dev command sets GASTRO_DEV=1 in the child process environment.
func IsDev() bool {
	return os.Getenv("GASTRO_DEV") == "1"
}

// DevRoot returns the project root directory to use when resolving dev-mode
// paths. When GASTRO_DEV_ROOT is set it is used directly; otherwise the
// current working directory is assumed.
//
// This matters for embedded-package projects where the process is started
// from a different directory than the gastro project root. For example:
//
//	cd cmd/myapp && GASTRO_DEV=1 GASTRO_DEV_ROOT=/path/to/internal/web go run .
func DevRoot() string {
	if root := os.Getenv("GASTRO_DEV_ROOT"); root != "" {
		return root
	}
	return "."
}

// GetTemplateFS returns the filesystem to use for loading templates.
// In dev mode, templates are read from disk so changes are picked up
// without a restart. In production, templates come from the embedded FS.
//
// When GASTRO_DEV_ROOT is set, template files are resolved relative to
// that directory instead of the process working directory.
func GetTemplateFS(embeddedFS fs.FS) fs.FS {
	if IsDev() {
		return os.DirFS(filepath.Join(DevRoot(), ".gastro", "templates"))
	}
	sub, _ := fs.Sub(embeddedFS, "templates")
	return sub
}

// GetStaticFS returns the filesystem to use for serving static assets.
// In dev mode, assets are read from disk. In production, they come from
// the embedded FS.
//
// When GASTRO_DEV_ROOT is set, static files are resolved relative to
// that directory instead of the process working directory.
func GetStaticFS(embeddedFS fs.FS) fs.FS {
	if IsDev() {
		return os.DirFS(filepath.Join(DevRoot(), "static"))
	}
	sub, _ := fs.Sub(embeddedFS, "static")
	return sub
}
