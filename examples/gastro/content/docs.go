package content

// Code examples for documentation pages.
// These are passed as props to the CodeBlock component which renders them
// inside {{ .Code }} (auto-escaped by html/template), so raw angle brackets
// are correct here.

// Landing page examples

const LandingComponentExample = `---
type Props struct {
    Title  string
    Author string
}

Title := gastro.Props().Title
Author := gastro.Props().Author
---
<article>
    <h2>{{ .Title }}</h2>
    <p>By {{ .Author }}</p>
</article>`

const LandingBuildExample = `# Generate Go code from .gastro files
gastro generate

# Build a single binary
go build -o myapp .

# Run it
./myapp`

// Getting Started examples

const GettingStartedInstall = `# Build the gastro CLI from source
go build -o gastro ./cmd/gastro/

# Or with mise (managed tooling)
mise install`

const GettingStartedProjectStructure = `myapp/
  pages/
    index.gastro
  static/
    styles.css
  main.go
  go.mod`

const GettingStartedFirstPage = `---
import "time"

Title := "Hello, Gastro"
Year := time.Now().Year()
---
<!DOCTYPE html>
<html>
<head><title>{{ .Title }}</title></head>
<body>
    <h1>{{ .Title }}</h1>
    <p>Copyright {{ .Year }}</p>
</body>
</html>`

const GettingStartedMainGo = `package main

import (
    "fmt"
    "net/http"
    "os"

    gastro "myapp/.gastro"
)

func main() {
    port := os.Getenv("PORT")
    if port == "" {
        port = "4242"
    }
    fmt.Printf("Listening on http://localhost:%s\n", port)
    http.ListenAndServe(":"+port, gastro.Routes())
}`

const GettingStartedBuildRun = `# Generate Go code from .gastro files
gastro generate

# Build the binary
go build -o myapp .

# Run
./myapp`

const GettingStartedDevMode = `# Watches for changes, rebuilds, restarts server
gastro dev`

// Pages & Routing examples

const PagesBasicPage = `---
ctx := gastro.Context()

Title := "Hello"
---
<h1>{{ .Title }}</h1>`

const PagesStaticPage = `---
import Layout "components/layout.gastro"

Title := "About"
---
{{ wrap Layout (dict "Title" .Title) }}
    <h1>About Me</h1>
{{ end }}`

const PagesDataFlow = `---
ctx := gastro.Context()
posts, err := db.ListPublished()
if err != nil {
    ctx.Error(500, "Failed to load posts")
    return
}

Posts := posts
Title := "Blog"
---
<h1>{{ .Title }}</h1>
{{ range .Posts }}
<p>{{ .Title }}</p>
{{ end }}`

const PagesImports = `---
import (
    "myblog/db"

    Layout "components/layout.gastro"
    PostCard "components/post-card.gastro"
)

ctx := gastro.Context()
posts, _ := db.ListPublished()
Posts := posts
---
{{ wrap Layout (dict "Title" "Home") }}
    {{ range .Posts }}
    {{ PostCard (dict "Title" .Title "Slug" .Slug) }}
    {{ end }}
{{ end }}`

const PagesDynamicRoute = `---
import (
    "myblog/db"
    Layout "components/layout.gastro"
)

ctx := gastro.Context()
slug := ctx.Param("slug")

post, err := db.GetBySlug(slug)
if err != nil {
    ctx.Error(404, "Post not found")
    return
}

Post := post
Title := post.Title
---
{{ wrap Layout (dict "Title" .Title) }}
    <article>
        <h1>{{ .Post.Title }}</h1>
        <p class="meta">By {{ .Post.Author }}</p>
        <div>{{ .Post.Body | safeHTML }}</div>
    </article>
{{ end }}`

const PagesContextRedirect = `---
ctx := gastro.Context()

user := getUser(ctx.Request())
if user == nil {
    ctx.Redirect("/login", 302)
    return
}

Name := user.Name
---
<h1>Welcome, {{ .Name }}</h1>`

const PagesContextQuery = `---
ctx := gastro.Context()
Name := ctx.Query("name")
---
<p>Hello, {{ .Name }}</p>`

const PagesContextHeader = `---
ctx := gastro.Context()
ctx.Header("Cache-Control", "public, max-age=3600")

Title := "Cached Page"
---
<h1>{{ .Title }}</h1>`

// Component examples

const ComponentBasic = `---
type Props struct {
    Title  string
    Author string
}

Title := gastro.Props().Title
Author := gastro.Props().Author
---
<article>
    <h2>{{ .Title }}</h2>
    <p>By {{ .Author }}</p>
</article>`

const ComponentComputed = `---
import "fmt"

type Props struct {
    Label string
    X     int
}

p := gastro.Props()
Label := p.Label
CX := fmt.Sprintf("%d", p.X + 135)
---
<text x="{{ .CX }}">{{ .Label }}</text>`

const ComponentImportUsage = `---
import (
    Layout "components/layout.gastro"
    PostCard "components/post-card.gastro"
)

ctx := gastro.Context()
---
{{ wrap Layout (dict "Title" "Home") }}
    {{ PostCard (dict "Title" "My Post" "Slug" "my-post") }}
{{ end }}`

const ComponentSlot = `---
type Props struct {
    Title string
}

Title := gastro.Props().Title
---
<html>
<head><title>{{ .Title }}</title></head>
<body>
    <nav>...</nav>
    <main>
        {{ .Children }}
    </main>
    <footer>...</footer>
</body>
</html>`

const ComponentSlotUsage = `{{ wrap Layout (dict "Title" "Home") }}
    <h1>Welcome</h1>
    <p>This replaces the slot.</p>
{{ end }}`

const ComponentPropSyntax = `<!-- Template expression -->
{{ PostCard (dict "Title" .Title "Slug" .Slug) }}

<!-- String literal -->
{{ Layout (dict "Title" "About") }}

<!-- Pipe expression -->
{{ PostCard (dict "Date" (.CreatedAt | timeFormat "Jan 2, 2006")) }}`

// SSE examples

const SSEBasicHandler = `func handleUpdates(w http.ResponseWriter, r *http.Request) {
    sse := gastro.NewSSE(w, r)

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-sse.Context().Done():
            return
        case <-ticker.C:
            now := time.Now().Format("15:04:05")
            sse.Send("time", now)
        }
    }
}`

const SSEDatastarHandler = `var count atomic.Int64

func handleIncrement(w http.ResponseWriter, r *http.Request) {
    n := count.Add(1)

    html, err := gastro.Render.Counter(
        gastro.CounterProps{Count: int(n)},
    )
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    sse := datastar.NewSSE(w, r)
    sse.PatchElements(html)
}`

const SSEDatastarPage = `---
import Layout "components/layout.gastro"
Title := "Counter"
---
{{ wrap Layout (dict "Title" .Title) }}
    <div id="count">0</div>
    <button data-on:click="@get('/api/increment')">+1</button>
{{ end }}`

const SSEMainGo = `func main() {
    mux := http.NewServeMux()

    // API/SSE endpoints first
    mux.HandleFunc("GET /api/increment", handleIncrement)
    mux.HandleFunc("GET /api/clock", handleClock)

    // Gastro page routes (catch-all)
    mux.Handle("/", gastro.Routes())

    http.ListenAndServe(":4242", mux)
}`

const SSERenderTyped = `// Each component gets a typed Render method
html, err := gastro.Render.Counter(
    gastro.CounterProps{Count: 42},
)

// Components with slots accept optional children
inner, _ := gastro.Render.Counter(
    gastro.CounterProps{Count: 42},
)
full, _ := gastro.Render.Layout(
    gastro.LayoutProps{Title: "Dashboard"},
    template.HTML(inner),
)`

const SSEPatchOptions = `sse.PatchElements(html,
    datastar.WithSelector("#dashboard"),
    datastar.WithMode(datastar.ModeInner),
)

sse.PatchSignals(map[string]any{
    "count": 42, "loading": false,
})

sse.RemoveElement("#toast-1")`

// Template Helpers examples

const HelpersStringFuncs = `{{ .Name | upper }}        {{/* "ALICE" */}}
{{ .Name | lower }}        {{/* "alice" */}}
{{ .Bio | trim }}          {{/* trims whitespace */}}
{{ .Tags | join ", " }}    {{/* "go, web, ssr" */}}`

const HelpersSafeFuncs = `{{/* Render trusted HTML */}}
{{ .Body | safeHTML }}

{{/* Safe attribute values */}}
<div class="{{ .Class | safeAttr }}">

{{/* Safe URLs */}}
<a href="{{ .URL | safeURL }}">

{{/* Safe CSS */}}
<div style="{{ .Style | safeCSS }}">

{{/* Safe JS */}}
<script>var x = {{ .Data | safeJS }}</script>`

const HelpersUtilityFuncs = `{{/* Default values */}}
{{ .Name | default "Anonymous" }}

{{/* Time formatting */}}
{{ .CreatedAt | timeFormat "Jan 2, 2006" }}

{{/* JSON output */}}
{{ .Config | json }}

{{/* Build maps and lists inline */}}
{{ dict "key" "value" "other" 42 }}
{{ list "a" "b" "c" }}

{{/* String operations */}}
{{ split .Tags "," }}
{{ contains .Title "Go" }}
{{ replace .Text "old" "new" }}`

const HelpersCustom = `routes := gastro.Routes(
    gastro.WithFuncs(template.FuncMap{
        "formatEUR": func(cents int) string {
            return fmt.Sprintf("%.2f EUR", float64(cents)/100)
        },
        "slugify": func(s string) string {
            return strings.ToLower(strings.ReplaceAll(s, " ", "-"))
        },
    }),
)`

// Deployment examples

const DeployBuild = `# Generate Go code from .gastro files
gastro generate

# Cross-compile for Linux
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/myapp .

# Deploy the single binary
scp dist/myapp server:/opt/myapp`

const DeployOneLiner = `# Or use gastro build for generate + compile
gastro build
./app`

const DeployDockerfile = `FROM golang:1.26-alpine AS build
WORKDIR /src

# Install the gastro CLI
COPY . /gastro-src
RUN cd /gastro-src && go build -o /usr/local/bin/gastro ./cmd/gastro/

# Copy project files
COPY examples/gastro/ .

# Generate and build
RUN gastro generate
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app .

FROM alpine:3
RUN adduser -D -u 1000 appuser
USER appuser
COPY --from=build /app /app
EXPOSE 4242
CMD ["/app"]`

const DeployEnvVars = `# Set the port via environment variable
PORT=8080 ./myapp

# In Docker
docker run -p 8080:8080 -e PORT=8080 myapp`
