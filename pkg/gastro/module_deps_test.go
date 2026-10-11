package gastro_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestCoreModule_HasNoThirdPartyDependencies guards the promise that the core
// gastro module stays (nearly) dependency-free. Integrations with third-party
// libraries — such as the Datastar SDK — live in nested modules
// (github.com/andrioid/gastro/datastar) so adopters of core never download
// them. Adding a module here is a deliberate decision that needs approval.
func TestCoreModule_HasNoThirdPartyDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to the go command")
	}

	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	modPath := strings.TrimSpace(string(gomod))
	if !strings.HasSuffix(modPath, "go.mod") {
		t.Fatalf("not running inside a module: GOMOD=%q", modPath)
	}

	cmd := exec.Command("go", "list", "-m", "-f", "{{.Path}}", "all")
	cmd.Dir = strings.TrimSuffix(modPath, "go.mod")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m all: %v", err)
	}

	got := strings.Fields(string(out))
	slices.Sort(got)
	want := []string{
		"github.com/andrioid/gastro",
		"github.com/google/shlex",
	}
	if !slices.Equal(got, want) {
		t.Errorf("core module graph changed:\ngot:  %v\nwant: %v", got, want)
	}
}
