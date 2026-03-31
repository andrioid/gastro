package sourcemap_test

import (
	"testing"

	"github.com/andrioid/gastro/internal/lsp/sourcemap"
)

func TestSourceMap_GastroToVirtual(t *testing.T) {
	// .gastro file:
	// line 1: ---
	// line 2: import "fmt"      <- frontmatter line 1
	// line 3: Title := "Hello"  <- frontmatter line 2
	// line 4: ---
	// line 5: <h1>...</h1>      <- template line 1

	// Virtual .go file wraps frontmatter with package + func:
	// line 1: package __gastro
	// line 2: import "fmt"
	// line 3: func __handler() {
	// line 4: Title := "Hello"    <- this is gastro line 3
	// line 5: }

	sm := sourcemap.New(2, 4) // frontmatter starts at gastro line 2, wrapper adds 2 lines before body

	// Gastro line 2 (first frontmatter line) -> virtual line 2 (after package, import is separate)
	// With offset=2 (wrapper lines before frontmatter body code)
	// gastro line 2 -> virtual line 2+0 = 2? No — let's think about this differently.

	// The source map needs: given a line in the virtual .go file, what's the
	// corresponding line in the .gastro file?
	gastroLine := sm.VirtualToGastro(4) // virtual line 4
	// frontmatter starts at gastro line 2, virtual offset is 2
	// so virtual line 4 = gastro line 2 + (4 - 2) = gastro line 4? No.
	// Let's simplify: virtual line N -> gastro line (N - virtualOffset + gastroFrontmatterStart)

	// Actually let's define it more clearly:
	// gastroFrontmatterStart = 2 (line after first ---)
	// virtualFrontmatterStart = 3 (line after package + func declaration)
	// So: gastro = virtual - virtualFrontmatterStart + gastroFrontmatterStart

	sm2 := sourcemap.New(2, 3)          // gastro fm starts at 2, virtual fm starts at 3
	gastroLine = sm2.VirtualToGastro(3) // virtual line 3 -> gastro line 2
	if gastroLine != 2 {
		t.Errorf("VirtualToGastro(3): got %d, want 2", gastroLine)
	}

	gastroLine = sm2.VirtualToGastro(5) // virtual line 5 -> gastro line 4
	if gastroLine != 4 {
		t.Errorf("VirtualToGastro(5): got %d, want 4", gastroLine)
	}
}

func TestSourceMap_GastroToVirtual_Direction(t *testing.T) {
	sm := sourcemap.New(2, 3) // gastro fm at line 2, virtual fm at line 3

	virtualLine := sm.GastroToVirtual(2) // gastro line 2 -> virtual line 3
	if virtualLine != 3 {
		t.Errorf("GastroToVirtual(2): got %d, want 3", virtualLine)
	}

	virtualLine = sm.GastroToVirtual(4) // gastro line 4 -> virtual line 5
	if virtualLine != 5 {
		t.Errorf("GastroToVirtual(4): got %d, want 5", virtualLine)
	}
}

func TestSourceMap_Roundtrip(t *testing.T) {
	sm := sourcemap.New(2, 3)

	for gastro := 2; gastro <= 10; gastro++ {
		virtual := sm.GastroToVirtual(gastro)
		back := sm.VirtualToGastro(virtual)
		if back != gastro {
			t.Errorf("roundtrip gastro %d -> virtual %d -> gastro %d", gastro, virtual, back)
		}
	}
}
