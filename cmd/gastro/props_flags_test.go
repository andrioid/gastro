package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCompileFlags(t *testing.T) {
	got, err := parseCompileFlags("generate", []string{"--typecheck-props"})
	if err != nil || !got.TypecheckProps {
		t.Fatalf("--typecheck-props: got %+v, %v", got, err)
	}
	got, err = parseCompileFlags("generate", nil)
	if err != nil || got.TypecheckProps {
		t.Fatalf("no flags: got %+v, %v", got, err)
	}
	_, err = parseCompileFlags("check", []string{"--typecheck"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag --typecheck") {
		t.Fatalf("expected unknown-flag error, got %v", err)
	}
}

func TestParseDevFlags_TypecheckProps(t *testing.T) {
	got, err := parseDevFlags([]string{"--typecheck-props", "--watch", "i18n/*.po"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.TypecheckProps || !equalStringSlices(got.Watch, []string{"i18n/*.po"}) {
		t.Errorf("got %+v", got)
	}
}

func TestParseWatchArgs_TypecheckProps(t *testing.T) {
	fl, err := parseWatchArgs([]string{"--run", "x", "--typecheck-props"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !fl.TypecheckProps {
		t.Error("TypecheckProps not set")
	}
}

// writeDevLoopProject writes a project whose page passes the given dict
// to a Card with a required Title and an int Count.
func writeDevLoopProject(t *testing.T, dict string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module devloopproject\n\ngo 1.27\n",
		"components/card.gastro": "---\ntype Props struct {\n\tTitle string `gastro:\"required\"`\n\tCount int\n}\n" +
			"Title := gastro.Props().Title\nCount := gastro.Props().Count\n---\n<p>{{ .Title }} {{ .Count }}</p>\n",
		"pages/index.gastro": "---\nimport Card \"components/card.gastro\"\n---\n{{ Card (dict " + dict + ") }}\n",
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDevLoopGenerate_OmittedRequiredPropFails(t *testing.T) {
	chdirT(t, writeDevLoopProject(t, `"Count" 1`))

	_, err := generateForDevLoop(compileFlags{})()
	if err == nil || !strings.Contains(err.Error(), `missing required prop "Title"`) {
		t.Fatalf("expected the dev loop generate to fail on the omitted required prop, got %v", err)
	}
	signal, readErr := os.ReadFile(filepath.Join(".gastro", ".build-error"))
	if readErr != nil || !strings.Contains(string(signal), "pages/index.gastro:4:") {
		t.Errorf("expected a positioned build-error signal for the browser, got %q (%v)", signal, readErr)
	}
}

func TestDevLoopGenerate_TypecheckProps(t *testing.T) {
	chdirT(t, writeDevLoopProject(t, `"Title" "x" "Count" "three"`))

	if _, err := generateForDevLoop(compileFlags{})(); err != nil {
		t.Fatalf("without --typecheck-props the literal passes: %v", err)
	}
	_, err := generateForDevLoop(compileFlags{TypecheckProps: true})()
	if err == nil || !strings.Contains(err.Error(), `prop "Count" on component Card`) {
		t.Fatalf("expected a typecheck error with --typecheck-props, got %v", err)
	}
}
