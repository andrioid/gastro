package gastro

import (
	"io/fs"
	"os"
)

// IsDev reports whether the application is running in development mode.
// The gastro dev command sets GASTRO_DEV=1 in the child process environment.
func IsDev() bool {
	return os.Getenv("GASTRO_DEV") == "1"
}

// GetTemplateFS returns the filesystem to use for loading templates.
// In dev mode, templates are read from disk so changes are picked up
// without a restart. In production, templates come from the embedded FS.
func GetTemplateFS(embeddedFS fs.FS) fs.FS {
	if IsDev() {
		return os.DirFS(".gastro/templates")
	}
	sub, _ := fs.Sub(embeddedFS, "templates")
	return sub
}

// GetStaticFS returns the filesystem to use for serving static assets.
// In dev mode, assets are read from disk. In production, they come from
// the embedded FS.
func GetStaticFS(embeddedFS fs.FS) fs.FS {
	if IsDev() {
		return os.DirFS("static")
	}
	sub, _ := fs.Sub(embeddedFS, "static")
	return sub
}
