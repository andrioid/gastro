// Package buildinfo reports which gastro release the running binary was
// built from. The version is stamped into every generated file's header
// so a .gastro/ tree can be matched to the binary that produced it.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

// Module is the gastro module path.
const Module = "github.com/andrioid/gastro"

// Devel is the version reported for builds that are not tied to a
// released gastro module version (a local checkout, or a module replaced
// by a directory).
const Devel = "(devel)"

// Version returns the gastro module version compiled into this binary:
// "v0.4.0" under `go tool gastro` or `go install …@v0.4.0`, and "(devel)"
// for local builds.
func Version() string {
	return version()
}

var version = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Devel
	}
	return fromBuildInfo(bi)
})

// fromBuildInfo finds the gastro module in bi. gastro is the main module
// when the binary is built inside the gastro repository or installed with
// `go install`, and a dependency when built as a tool of another module.
func fromBuildInfo(bi *debug.BuildInfo) string {
	if bi.Main.Path == Module {
		return moduleVersion(&bi.Main)
	}
	for _, dep := range bi.Deps {
		if dep.Path == Module {
			return moduleVersion(dep)
		}
	}
	return Devel
}

func moduleVersion(m *debug.Module) string {
	if m.Replace != nil {
		// A directory replacement has no version: it is a local checkout.
		m = m.Replace
	}
	if m.Version == "" {
		return Devel
	}
	return m.Version
}
