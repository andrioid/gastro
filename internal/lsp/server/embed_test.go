package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbedDirectiveAtLine(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		wantOK  bool
		wantArg string
	}{
		{"happy path", "//gastro:embed intro.md", true, "intro.md"},
		{"with leading whitespace", "    //gastro:embed intro.md", true, "intro.md"},
		{"with leading tabs", "\t\t//gastro:embed ../foo.md", true, "../foo.md"},
		{"trailing whitespace trimmed", "//gastro:embed intro.md   ", true, "intro.md"},
		{"directive only, no arg", "//gastro:embed", false, ""},
		{"directive with empty arg", "//gastro:embed ", false, ""},
		{"directive without separator", "//gastro:embedfoo.md", false, ""},
		{"comment of comment", "// //gastro:embed foo.md", false, ""},
		{"random text containing token", "x := \"//gastro:embed foo\"", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, _, _, ok := embedDirectiveAtLine(c.line)
			if ok != c.wantOK {
				t.Errorf("ok = %v, want %v", ok, c.wantOK)
			}
			if path != c.wantArg {
				t.Errorf("path = %q, want %q", path, c.wantArg)
			}
		})
	}
}

func TestResolveEmbedPathLSP_HappyPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pages := filepath.Join(root, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(pages, "page.gastro")
	if err := os.WriteFile(src, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pages, "intro.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveEmbedPathLSP("intro.md", src, root)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	wantReal, _ := filepath.EvalSymlinks(filepath.Join(pages, "intro.md"))
	if resolved != wantReal {
		t.Errorf("resolved=%s want=%s", resolved, wantReal)
	}
}

func TestResolveEmbedPathLSP_OutsideModule(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(pages, "page.gastro")
	if err := os.WriteFile(src, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveEmbedPathLSP("../../../etc/passwd", src, root)
	if err == nil {
		t.Fatal("expected error for path escaping module")
	}
}

func TestResolveEmbedPathLSP_MissingFile(t *testing.T) {
	root := t.TempDir()
	pages := filepath.Join(root, "pages")
	if err := os.MkdirAll(pages, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(pages, "page.gastro")
	if err := os.WriteFile(src, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveEmbedPathLSP("nope.md", src, root)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
