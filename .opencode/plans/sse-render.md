# SSE Component Rendering Plan

## Status: Blocked on component composition

The Render API depends on components being composable (component A using component B).
That must be fixed first -- see "Prerequisite" below.

## Goal

Allow gastro components to be rendered from SSE handlers with full type safety,
enabling use cases like dashboards where complex markup is re-rendered and morphed
via Datastar.

## Design: `gastro.Render` struct pattern

Components get exported render methods on a `Render` struct. Props types are
exported at package level.

### Usage

```go
import (
    gastro "myapp/.gastro"
    "github.com/andrioid/gastro/pkg/gastro/datastar"
)

func handleIncrement(w http.ResponseWriter, r *http.Request) {
    n := count.Add(1)

    html, err := gastro.Render.Counter(gastro.CounterProps{Count: int(n)})
    if err != nil { return }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
}
```

### What gets generated

In the component's `.go` file (e.g. `.gastro/components_counter.go`):

```go
// Existing (internal, used for template composition)
type componentCounterProps struct { Count int }
func componentCounter(propsMap map[string]any) template.HTML { ... }

// NEW: exported Props alias
type CounterProps = componentCounterProps
```

In `.gastro/render.go`:

```go
var Render = &renderAPI{}
type renderAPI struct{}

func (r *renderAPI) Counter(props CounterProps) (string, error) {
    data := map[string]any{
        "Count": props.Count,
    }
    var buf bytes.Buffer
    if err := componentCounterTemplate.Execute(&buf, data); err != nil {
        return "", err
    }
    return buf.String(), nil
}
```

Components with children (slots) get a variadic `children` parameter:

```go
func (r *renderAPI) Layout(props LayoutProps, children ...template.HTML) (string, error) {
    data := map[string]any{
        "Title":    props.Title,
        "Children": template.HTML(""),
    }
    if len(children) > 0 {
        data["Children"] = children[0]
    }
    // ...
}
```

Components without a Props type get `map[string]any`:

```go
func (r *renderAPI) SimpleCard(data map[string]any) (string, error) { ... }
```

### Namespace result

- `gastro.Routes()` -- routing
- `gastro.Render.Counter(...)` -- component rendering
- `gastro.CounterProps{...}` -- type-safe props (at package level, Go requirement)

### Type safety

| What             | Safety       |
|------------------|-------------|
| Function name    | Compile-time (method exists or doesn't) |
| Props fields     | Compile-time (struct fields checked by Go compiler) |
| Props types      | Compile-time (Go type system) |
| Template name    | N/A (no string lookup, methods are per-component) |

### Files to change

1. `internal/codegen/generate.go` -- Add exported Props alias to `componentTmpl`.
   Add `ExportedName` field to `generateData`.
2. `internal/compiler/compiler.go` -- Collect component metadata during compilation.
   Generate `renderAPI` struct and methods in `render.go`.
3. `internal/codegen/generate_test.go` -- Test exported Props alias generation.
4. `internal/compiler/compiler_test.go` -- End-to-end test for Render function.
5. `docs/sse.md` -- Update with Render pattern.
6. `examples/sse/` -- Update example to use Render.

### Open questions

- Should components without Props get a Render method at all?
- How to handle component-specific frontmatter logic in Render methods?
  (e.g., if frontmatter computes derived values from props)

---

## Prerequisite: Component composition

Components cannot currently use other components. The `componentTmpl` in
`generate.go` does not wire up the FuncMap for `use` declarations even though
the parser and template transformer handle them correctly.

### What needs to change

**Single file: `internal/codegen/generate.go`**

The `componentTmpl` needs the same conditional `init()` pattern that
`handlerTmpl` uses when `.Uses` is non-empty.

Currently (line 173):
```go
var funcNameTemplate = template.Must(
    template.New("funcName").Funcs(gastroRuntime.DefaultFuncs()).Parse(`...`))
```

Needs to become (pseudocode showing the conditional):
```
if .Uses:
    var funcNameTemplate = template.New("funcName")

    func init() {
        __fm := gastroRuntime.DefaultFuncs()
        // for each use:
        __fm["__gastro_Name"] = funcName
        // end for
        __fm["__gastro_render_children"] = func(name string, data any) template.HTML {
            var __buf bytes.Buffer
            funcNameTemplate.ExecuteTemplate(&__buf, name, data)
            return template.HTML(__buf.String())
        }
        template.Must(funcNameTemplate.Funcs(__fm).Parse(`...`))
    }
else:
    var funcNameTemplate = template.Must(
        template.New("funcName").Funcs(gastroRuntime.DefaultFuncs()).Parse(`...`))
end
```

### Why this works without ordering concerns

All generated files are in the same Go package. Component functions
(componentBadge, etc.) are package-level declarations. Go init() runs
after all package-level vars are initialized. No compilation order issues.

### Tests needed

1. `internal/codegen/generate_test.go`: TestGenerate_ComponentWithUses
2. `internal/compiler/compiler_test.go`: End-to-end with nested components
3. `internal/compiler/testdata/`: Test fixtures for component composition

### What already works (no changes needed)

- Parser: extracts `use` from components correctly
- Template transformer: transforms component tags identically for pages/components
- Compiler: passes file.Uses through for all files
- Router: unaffected (components are not routed)
