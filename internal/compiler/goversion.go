package compiler

import (
	"fmt"
	"go/version"
	"os"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
)

// MinGoVersion is the lowest `go` directive a consumer module may
// declare. Generated code uses language features from this release
// (generic methods), so an older directive would surface as a compiler
// error inside .gastro/ instead of a clear message from gastro.
const MinGoVersion = "1.27"

// impliedGoVersion is the language version the go command assumes for a
// go.mod that has no go directive.
const impliedGoVersion = "1.16"

// checkGoDirective returns an error when the go.mod governing
// absProjectDir declares a go version below MinGoVersion. A project
// outside any module is not checked: it cannot build the generated code
// either way, and the go command reports that more precisely.
func checkGoDirective(absProjectDir string) error {
	root := codegen.FindModuleRoot(absProjectDir)
	if root == "" {
		return nil
	}
	goModPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", goModPath, err)
	}

	declared, found := goDirective(data)
	if !found {
		return fmt.Errorf("%s has no go directive (the go command then assumes go %s); gastro requires go %s or later: run `go mod edit -go=%s` in %s",
			goModPath, impliedGoVersion, MinGoVersion, MinGoVersion, root)
	}
	if !version.IsValid("go" + declared) {
		return fmt.Errorf("%s declares an invalid go directive %q; gastro requires go %s or later", goModPath, declared, MinGoVersion)
	}
	if version.Compare("go"+declared, "go"+MinGoVersion) < 0 {
		return fmt.Errorf("%s declares go %s; gastro requires go %s or later: run `go mod edit -go=%s` in %s",
			goModPath, declared, MinGoVersion, MinGoVersion, root)
	}
	return nil
}

// goDirective returns the version from the go.mod `go` directive.
func goDirective(goMod []byte) (string, bool) {
	for line := range strings.Lines(string(goMod)) {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			return fields[1], true
		}
	}
	return "", false
}
