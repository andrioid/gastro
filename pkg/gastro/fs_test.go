package gastro_test

import (
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"github.com/andrioid/gastro/pkg/gastro"
)

func TestIsDev_ReturnsFalseByDefault(t *testing.T) {
	t.Setenv("GASTRO_DEV", "")
	if gastro.IsDev() {
		t.Error("IsDev() should return false when GASTRO_DEV is not set")
	}
}

func TestIsDev_ReturnsTrueWhenSet(t *testing.T) {
	t.Setenv("GASTRO_DEV", "1")
	if !gastro.IsDev() {
		t.Error("IsDev() should return true when GASTRO_DEV=1")
	}
}

func TestIsDev_ReturnsFalseForOtherValues(t *testing.T) {
	t.Setenv("GASTRO_DEV", "true")
	if gastro.IsDev() {
		t.Error("IsDev() should return false when GASTRO_DEV is not exactly '1'")
	}
}

func TestGetTemplateFS_UsesEmbeddedInProduction(t *testing.T) {
	t.Setenv("GASTRO_DEV", "")

	embedded := fstest.MapFS{
		"templates/pages_index.html": &fstest.MapFile{Data: []byte("<h1>hello</h1>")},
	}

	tfs := gastro.GetTemplateFS(embedded)

	content, err := fs.ReadFile(tfs, "pages_index.html")
	if err != nil {
		t.Fatalf("reading from template FS: %v", err)
	}
	if string(content) != "<h1>hello</h1>" {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestGetTemplateFS_UsesDiskInDev(t *testing.T) {
	t.Setenv("GASTRO_DEV", "1")

	// Create a temporary template directory to read from
	dir := t.TempDir()
	templateDir := dir + "/.gastro/templates"
	os.MkdirAll(templateDir, 0o755)
	os.WriteFile(templateDir+"/pages_index.html", []byte("<h1>dev</h1>"), 0o644)

	// GetTemplateFS reads from .gastro/templates relative to cwd
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	embedded := fstest.MapFS{
		"templates/pages_index.html": &fstest.MapFile{Data: []byte("<h1>prod</h1>")},
	}

	tfs := gastro.GetTemplateFS(embedded)

	content, err := fs.ReadFile(tfs, "pages_index.html")
	if err != nil {
		t.Fatalf("reading from template FS: %v", err)
	}
	if string(content) != "<h1>dev</h1>" {
		t.Errorf("expected dev content, got: %q", string(content))
	}
}

func TestGetStaticFS_UsesEmbeddedInProduction(t *testing.T) {
	t.Setenv("GASTRO_DEV", "")

	embedded := fstest.MapFS{
		"static/styles.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	sfs := gastro.GetStaticFS(embedded)

	content, err := fs.ReadFile(sfs, "styles.css")
	if err != nil {
		t.Fatalf("reading from static FS: %v", err)
	}
	if string(content) != "body{}" {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestGetStaticFS_UsesDiskInDev(t *testing.T) {
	t.Setenv("GASTRO_DEV", "1")

	dir := t.TempDir()
	os.MkdirAll(dir+"/static", 0o755)
	os.WriteFile(dir+"/static/styles.css", []byte("body{color:red}"), 0o644)

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	embedded := fstest.MapFS{
		"static/styles.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	sfs := gastro.GetStaticFS(embedded)

	content, err := fs.ReadFile(sfs, "styles.css")
	if err != nil {
		t.Fatalf("reading from static FS: %v", err)
	}
	if string(content) != "body{color:red}" {
		t.Errorf("expected dev content, got: %q", string(content))
	}
}

func TestDevRoot_DefaultsToCurrentDir(t *testing.T) {
	t.Setenv("GASTRO_DEV_ROOT", "")
	if got := gastro.DevRoot(); got != "." {
		t.Errorf("DevRoot() = %q, want %q", got, ".")
	}
}

func TestDevRoot_HonoursEnvVar(t *testing.T) {
	t.Setenv("GASTRO_DEV_ROOT", "/some/project")
	if got := gastro.DevRoot(); got != "/some/project" {
		t.Errorf("DevRoot() = %q, want %q", got, "/some/project")
	}
}

func TestGetTemplateFS_HonoursDevRoot(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/.gastro/templates", 0o755)
	os.WriteFile(dir+"/.gastro/templates/pages_index.html", []byte("<h1>root</h1>"), 0o644)

	t.Setenv("GASTRO_DEV", "1")
	t.Setenv("GASTRO_DEV_ROOT", dir)

	embedded := fstest.MapFS{
		"templates/pages_index.html": &fstest.MapFile{Data: []byte("<h1>prod</h1>")},
	}

	tfs := gastro.GetTemplateFS(embedded)
	content, err := fs.ReadFile(tfs, "pages_index.html")
	if err != nil {
		t.Fatalf("reading from template FS: %v", err)
	}
	if string(content) != "<h1>root</h1>" {
		t.Errorf("expected content from GASTRO_DEV_ROOT, got: %q", content)
	}
}

func TestGetStaticFS_HonoursDevRoot(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/static", 0o755)
	os.WriteFile(dir+"/static/styles.css", []byte("body{devroot:1}"), 0o644)

	t.Setenv("GASTRO_DEV", "1")
	t.Setenv("GASTRO_DEV_ROOT", dir)

	embedded := fstest.MapFS{
		"static/styles.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	sfs := gastro.GetStaticFS(embedded)
	content, err := fs.ReadFile(sfs, "styles.css")
	if err != nil {
		t.Fatalf("reading from static FS: %v", err)
	}
	if string(content) != "body{devroot:1}" {
		t.Errorf("expected content from GASTRO_DEV_ROOT, got: %q", content)
	}
}
