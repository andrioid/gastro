# Template Helpers

Gastro provides 21 built-in template functions available in all templates without registration. You can also add custom helpers.

## String Functions

```go
{{ .Name | upper }}        // "ALICE"
{{ .Name | lower }}        // "alice"
{{ .Bio | trim }}          // trims whitespace
{{ .Tags | join ", " }}    // "go, web, ssr"
```

| Function | Description |
|----------|-------------|
| `upper` | Converts string to uppercase |
| `lower` | Converts string to lowercase |
| `trim` | Trims leading and trailing whitespace |
| `join` | Joins a slice of strings with a separator |
| `split` | Splits a string by separator |
| `contains` | Checks if a string contains a substring |
| `replace` | Replaces occurrences in a string |

## Safety Functions

These functions mark content as safe for specific contexts, bypassing `html/template`'s automatic escaping. Use them only with trusted content:

```go
// Render trusted HTML
{{ .Body | safeHTML }}

// Safe attribute values
<div class="{{ .Class | safeAttr }}">

// Safe URLs
<a href="{{ .URL | safeURL }}">

// Safe CSS
<div style="{{ .Style | safeCSS }}">

// Safe JS
<script>var x = {{ .Data | safeJS }}</script>
```

| Function | Marks safe for |
|----------|----------------|
| `safeHTML` | HTML content (renders without escaping) |
| `safeAttr` | HTML attribute values |
| `safeURL` | URL values in `href`/`src` attributes |
| `safeCSS` | CSS property values |
| `safeJS` | JavaScript values |

## Utility Functions

```go
// Default values
{{ .Name | default "Anonymous" }}

// Time formatting
{{ .CreatedAt | timeFormat "Jan 2, 2006" }}

// JSON output
{{ .Config | json }}

// Build maps and lists inline
{{ dict "key" "value" "other" 42 }}
{{ list "a" "b" "c" }}

// String operations
{{ split .Tags "," }}
{{ contains .Title "Go" }}
{{ replace .Text "old" "new" }}
```

| Function | Description |
|----------|-------------|
| `default` | Returns value, or fallback if empty/zero |
| `timeFormat` | Formats a `time.Time` using Go's layout syntax |
| `json` | JSON-encodes a value |
| `dict` | Creates a `map[string]any` from key-value pairs |
| `list` | Creates a `[]any` from arguments |

## Membership and Lookup

Templates often need to ask "is this thing in that thing?" — e.g. "is this
tab the active one?", "does this dict carry an optional field?". Without
helpers, authors end up declaring `activeSet := map[string]bool{...}` in
frontmatter; these helpers let templates ask the question directly.

```go
// Slice membership
{{ if has .Tag .ActiveTags }}<span class="active">{{ end }}

// Variadic form (no slice needed)
{{ if has .Status "open" "in_progress" }}⚠️{{ end }}

// Map key presence (works against any map)
{{ if hasKey "Avatar" .User }}<img src="{{ .User.Avatar }}">{{ end }}

// The active-set idiom: build a set once, query repeatedly.
{{ $active := set "home" "about" "contact" }}
{{ range .Tabs }}
  <a class="{{ if hasKey . $active }}active{{ end }}">{{ . }}</a>
{{ end }}
```

| Function | Description |
|----------|-------------|
| `has` | Reports whether `needle` appears in `haystack`. Accepts a slice/array or variadic arguments. Uses `reflect.DeepEqual`. |
| `hasKey` | Reports whether `key` is present in `m`. Works on any map (string-keyed, int-keyed, `map[any]bool`). Returns false for non-maps rather than panicking. |
| `set` | Builds a `map[any]bool` from the given items. Combine with `hasKey` for efficient repeated membership tests. Unhashable items (slices, maps, funcs) are skipped silently. |

## Custom Helpers

Register custom template functions in your `main.go` using `gastro.WithFuncs()`:

```go
router := gastro.New(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
        "slugify": func(s string) string {
            return strings.ToLower(strings.ReplaceAll(s, " ", "-"))
        },
    }),
)
http.ListenAndServe(":4242", router.Handler())
```

Custom functions are available in all pages and components, just like the built-in helpers.
