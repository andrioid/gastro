package compiler

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/andrioid/gastro/internal/codegen"
)

// GeneratedVersion returns the gastro version stamped in the header of a
// generated tree (read from outputDir/routes.go, which every generation
// writes). It returns "" for a tree generated before version stamps
// existed. Read errors, including a missing routes.go, are returned.
func GeneratedVersion(outputDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(outputDir, "routes.go"))
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(string(data), "\n")
	version, ok := codegen.ParseGeneratedHeader(first)
	if !ok {
		return "", nil
	}
	return version, nil
}
