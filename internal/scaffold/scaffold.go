package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// GoVersion is the Go version used in generated go.mod files.
// Must match the version in the project root go.mod and mise.toml.
const GoVersion = "1.26.1"

//go:embed all:template
var templateFS embed.FS

type templateData struct {
	ProjectName   string
	GastroVersion string
	GoVersion     string
	IsDev         bool
}

// Generate creates a new gastro project skeleton in targetDir.
// The projectName is used as the Go module name.
// The gastroVersion is used in the generated go.mod require directive
// (e.g. "0.1.1"). When set to "dev", a commented-out replace directive
// is included instead.
func Generate(projectName, targetDir, gastroVersion string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}

	ver := strings.TrimPrefix(gastroVersion, "v")
	data := templateData{
		ProjectName:   projectName,
		GastroVersion: ver,
		GoVersion:     GoVersion,
		IsDev:         ver == "" || ver == "dev",
	}

	root := "template"

	return fs.WalkDir(templateFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Relative path from the template root
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		dest := filepath.Join(targetDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		content, err := templateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}

		// Process .tmpl files through text/template and strip the suffix
		if strings.HasSuffix(path, ".tmpl") {
			dest = strings.TrimSuffix(dest, ".tmpl")

			tmpl, err := template.New(filepath.Base(path)).Parse(string(content))
			if err != nil {
				return fmt.Errorf("parse template %s: %w", path, err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return fmt.Errorf("execute template %s: %w", path, err)
			}
			content = buf.Bytes()
		}

		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}

		return nil
	})
}
