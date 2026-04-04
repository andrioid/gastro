package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GoVersion is the Go version used in generated go.mod files.
// Must match the version in the project root go.mod and mise.toml.
const GoVersion = "1.26.1"

// Generate creates a new gastro project skeleton in targetDir.
// The projectName is used as the Go module name.
// The gastroVersion is used in the generated go.mod require directive
// (e.g. "0.1.1"). When set to "dev", a commented-out replace directive
// is included instead.
func Generate(projectName, targetDir, gastroVersion string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}

	dirs := []string{"pages", "components", "static"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(targetDir, d), 0o755); err != nil {
			return fmt.Errorf("create %s directory: %w", d, err)
		}
	}

	files := map[string]string{
		"pages/index.gastro": indexPage,
		"main.go":            mainGo(projectName),
		"go.mod":             goMod(projectName, gastroVersion),
		".gitignore":         gitIgnore,
		"static/.gitkeep":    "",
	}

	for name, content := range files {
		path := filepath.Join(targetDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	return nil
}

func mainGo(moduleName string) string {
	return `package main

import (
	"fmt"
	"net/http"
	"os"

	gastro "` + moduleName + `/.gastro"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4242"
	}

	routes := gastro.Routes()

	fmt.Printf("Listening on http://localhost:%s\n", port)
	http.ListenAndServe(":"+port, routes)
}
`
}

func goMod(moduleName, gastroVersion string) string {
	ver := strings.TrimPrefix(gastroVersion, "v")

	if ver == "" || ver == "dev" {
		return `module ` + moduleName + `

go ` + GoVersion + `

require github.com/andrioid/gastro v0.0.0

// For local development, uncomment and adjust the path:
// replace github.com/andrioid/gastro => ../gastro
`
	}

	return `module ` + moduleName + `

go ` + GoVersion + `

require github.com/andrioid/gastro v` + ver + `
`
}

const indexPage = `---
Title := "Welcome to Gastro"
---
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{ .Title }}</title>
</head>
<body>
    <h1>{{ .Title }}</h1>
    <p>Edit <code>pages/index.gastro</code> to get started.</p>
</body>
</html>
`

const gitIgnore = `.gastro/
app
`
