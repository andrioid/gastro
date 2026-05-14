package server

// Discovery and caching of WithRequestFuncs binder helper names per
// project. The LSP uses this to:
//
//   - feed template-parse stubs (so {{ t "..." }} parses without
//     "function not defined" errors) and
//   - surface request-aware helpers in completion / hover.
//
// Strategy: AST-parse the project's main.go (one-hop only — we look for
// gastro.New(...) and walk its WithRequestFuncs arguments). Each binder
// is one of:
//
//   1. A function literal that returns a literal template.FuncMap{...}.
//      Keys are extracted from the composite-literal Elts.
//   2. A reference to a named function. We follow one hop into the same
//      file's declarations (the function must be defined in main.go to
//      be found by this lightweight scan) and try the same extraction.
//   3. Anything else (dynamically built FuncMap, return from another
//      package, etc.). We record the call site so a diagnostics surface
//      can emit an info-level note explaining why these binders don't
//      contribute to completion/hover.
//
// The scan is intentionally narrow. Gopls handles deep static analysis
// of the binder Go code; here we just want a best-effort key list so the
// .gastro template editing experience matches the binder shape.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// requestFuncInfo describes a single request-aware helper discovered by
// scanning a project's main.go. Position is the *.go file path and the
// line/column the key string literal was declared on — used by hover
// and go-to-definition to jump precisely to the FuncMap entry.
type requestFuncInfo struct {
	Name     string // helper name as referenced from templates, e.g. "t"
	File     string // absolute path to the Go file where the key was declared
	Line     int    // 1-indexed line of the key string literal
	Column   int    // 1-indexed column of the key string literal's opening quote
	BinderID int    // 0-based index of the binder in registration order
}

// requestFuncsCache caches discovery results per project root. The
// cache is keyed by project root and invalidated when main.go's modtime
// changes. Lookups are race-safe.
type requestFuncsCache struct {
	mu      sync.RWMutex
	entries map[string]requestFuncsCacheEntry // projectRoot -> entry
	// publishedModTime tracks the most recent main.go modtime for which
	// the LSP has published info-level diagnostics covering non-literal
	// binder sites. Used to dedupe redundant publishes when many .gastro
	// files belong to the same project and each didOpen would otherwise
	// re-publish the same diagnostic set.
	publishedModTime map[string]int64 // projectRoot -> last-published modtime
}

type requestFuncsCacheEntry struct {
	mainModTime int64                      // last-observed main.go modtime in unix nanos
	helpers     map[string]requestFuncInfo // name -> info
	// nonLiteralBinders records call sites for binders that couldn't be
	// statically analyzed. The LSP can surface an info-level diagnostic
	// pointing at each site so adopters know why their helpers aren't
	// showing up in completion.
	nonLiteralBinders []requestFuncBinderSite
}

// requestFuncBinderSite describes a WithRequestFuncs call whose argument
// shape resisted static analysis (e.g. dynamically built FuncMap). The
// position (1-indexed line + column) anchors an info-level diagnostic
// at the call site.
type requestFuncBinderSite struct {
	File   string
	Line   int
	Column int
}

// newRequestFuncsCache constructs a cache. Safe to call multiple times;
// each call returns a fresh, independent cache.
func newRequestFuncsCache() *requestFuncsCache {
	return &requestFuncsCache{
		entries:          make(map[string]requestFuncsCacheEntry),
		publishedModTime: make(map[string]int64),
	}
}

// markPublished records that diagnostics have been published for
// projectRoot at the given main.go modtime. Subsequent calls to
// shouldPublish for the same root + modtime return false until the
// modtime changes (main.go edit). Race-safe.
func (c *requestFuncsCache) markPublished(projectRoot string, modTime int64) {
	c.mu.Lock()
	c.publishedModTime[projectRoot] = modTime
	c.mu.Unlock()
}

// shouldPublish reports whether the LSP should (re-)publish info
// diagnostics for projectRoot. Returns false when we've already
// published for this root at the same modtime.
func (c *requestFuncsCache) shouldPublish(projectRoot string, modTime int64) bool {
	c.mu.RLock()
	last, ok := c.publishedModTime[projectRoot]
	c.mu.RUnlock()
	return !ok || last != modTime
}

// modTimeFor returns the cached main.go modtime for projectRoot, or 0
// when no entry has been observed yet. Used by the diagnostic publisher
// to key its dedup gate against the same value Lookup keyed its
// staleness check against.
func (c *requestFuncsCache) modTimeFor(projectRoot string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if entry, ok := c.entries[projectRoot]; ok {
		return entry.mainModTime
	}
	return 0
}

// Lookup returns the discovered helpers for projectRoot, scanning if the
// cached entry is missing or stale. Staleness is detected via main.go's
// modtime: each call os.Stat's main.go and re-scans when the modtime
// differs from the cached entry. This keeps the cache coherent without
// requiring an explicit didChange wire-up.
func (c *requestFuncsCache) Lookup(projectRoot string) requestFuncsCacheEntry {
	mainPath := filepath.Join(projectRoot, "main.go")
	info, statErr := os.Stat(mainPath)
	var modTime int64
	if statErr == nil {
		modTime = info.ModTime().UnixNano()
	}

	c.mu.RLock()
	if existing, ok := c.entries[projectRoot]; ok && existing.mainModTime == modTime {
		c.mu.RUnlock()
		return existing
	}
	c.mu.RUnlock()

	// Compute outside the read lock; multiple goroutines may race but
	// each will produce an equivalent result and the write below is
	// idempotent for a given modTime.
	helpers, sites := scanRequestFuncs(mainPath)
	entry := requestFuncsCacheEntry{
		mainModTime:       modTime,
		helpers:           helpers,
		nonLiteralBinders: sites,
	}

	c.mu.Lock()
	c.entries[projectRoot] = entry
	c.mu.Unlock()

	return entry
}

// scanRequestFuncs reads mainPath and extracts WithRequestFuncs binder
// helper names. Returns an empty map and nil sites when the file can't
// be read or contains no recognisable binders.
func scanRequestFuncs(mainPath string) (map[string]requestFuncInfo, []requestFuncBinderSite) {
	src, err := os.ReadFile(mainPath)
	if err != nil {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainPath, src, parser.AllErrors)
	if err != nil {
		// Partial parse — try to use what we have. ParseFile returns a
		// non-nil file on most syntax errors.
		if file == nil {
			return nil, nil
		}
	}

	// Build a quick index of top-level func decls in this file so
	// reference-to-named-binder can be resolved without re-parsing.
	funcDecls := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name != nil {
			funcDecls[fd.Name.Name] = fd
		}
	}

	helpers := map[string]requestFuncInfo{}
	var sites []requestFuncBinderSite
	var binderIdx int

	// Walk every call expression looking for gastro.WithRequestFuncs(arg).
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !isGastroWithRequestFuncsCall(call) {
			return true
		}
		if len(call.Args) != 1 {
			return true
		}
		arg := call.Args[0]
		callPos := fset.Position(call.Pos())
		extracted, recognised := extractFuncMapKeys(arg, funcDecls, fset, callPos.Filename, binderIdx)
		if !recognised {
			sites = append(sites, requestFuncBinderSite{
				File:   callPos.Filename,
				Line:   callPos.Line,
				Column: callPos.Column,
			})
			binderIdx++
			return true
		}
		for name, info := range extracted {
			// First binder wins on collision. The runtime would panic at
			// New() anyway, so the LSP merely picks one position to
			// surface for hover/go-to-def.
			if _, exists := helpers[name]; !exists {
				helpers[name] = info
			}
		}
		binderIdx++
		return true
	})

	return helpers, sites
}

// isGastroWithRequestFuncsCall reports whether call is a CallExpr of the
// shape `gastro.WithRequestFuncs(...)`. The selector qualifier is
// required because that's the canonical adopter API surface; dot-imported
// usage is intentionally not supported (and would be unusual style).
func isGastroWithRequestFuncsCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel == nil || sel.Sel.Name != "WithRequestFuncs" {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkgIdent.Name == "gastro"
}

// extractFuncMapKeys tries to extract the FuncMap key set from a binder
// expression. Recognised shapes:
//
//   - *ast.FuncLit: function literal whose last statement is a return of
//     a CompositeLit of type template.FuncMap (or just FuncMap when the
//     adopter dot-imported html/template).
//   - *ast.Ident referencing a top-level *ast.FuncDecl in the same file:
//     same extraction applied to that decl's body.
//
// Returns (keys, true) when shape is recognised, (nil, false) otherwise.
func extractFuncMapKeys(
	expr ast.Expr,
	funcDecls map[string]*ast.FuncDecl,
	fset *token.FileSet,
	file string,
	binderIdx int,
) (map[string]requestFuncInfo, bool) {
	switch v := expr.(type) {
	case *ast.FuncLit:
		return extractFromFuncBody(v.Body, fset, file, binderIdx)
	case *ast.Ident:
		if fd, ok := funcDecls[v.Name]; ok && fd.Body != nil {
			return extractFromFuncBody(fd.Body, fset, file, binderIdx)
		}
	}
	return nil, false
}

// extractFromFuncBody scans body for a return statement whose value is a
// template.FuncMap composite literal and extracts its key set.
func extractFromFuncBody(
	body *ast.BlockStmt,
	fset *token.FileSet,
	file string,
	binderIdx int,
) (map[string]requestFuncInfo, bool) {
	var compositeLit *ast.CompositeLit

	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, r := range ret.Results {
			if cl, ok := r.(*ast.CompositeLit); ok && isFuncMapType(cl.Type) {
				compositeLit = cl
				return false
			}
		}
		return true
	})

	if compositeLit == nil {
		return nil, false
	}

	out := map[string]requestFuncInfo{}
	for _, elt := range compositeLit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		bl, ok := kv.Key.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			continue
		}
		name, err := strconv.Unquote(bl.Value)
		if err != nil {
			continue
		}
		pos := fset.Position(bl.Pos())
		out[name] = requestFuncInfo{
			Name:     name,
			File:     file,
			Line:     pos.Line,
			Column:   pos.Column,
			BinderID: binderIdx,
		}
	}
	return out, true
}

// isFuncMapType reports whether t is the type expression
// `template.FuncMap` (or `html/template.FuncMap` via package qualifier).
// We match by the selector's final identifier name; the qualifier can be
// `template` (the canonical alias) or any other import name the adopter
// chose.
func isFuncMapType(t ast.Expr) bool {
	switch tt := t.(type) {
	case *ast.SelectorExpr:
		return tt.Sel != nil && tt.Sel.Name == "FuncMap"
	case *ast.Ident:
		// Dot-imported template package — uncommon but tolerated.
		return tt.Name == "FuncMap"
	}
	return false
}

// requestFuncsNames returns just the sorted name list, convenient for
// callers that don't need positions.
func (e requestFuncsCacheEntry) Names() []string {
	if len(e.helpers) == 0 {
		return nil
	}
	out := make([]string, 0, len(e.helpers))
	for name := range e.helpers {
		out = append(out, name)
	}
	// Cheap deterministic order (insertion sort; tiny lists).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// HelperAt returns the requestFuncInfo for name, or zero value when the
// helper isn't known.
func (e requestFuncsCacheEntry) HelperAt(name string) (requestFuncInfo, bool) {
	if e.helpers == nil {
		return requestFuncInfo{}, false
	}
	info, ok := e.helpers[name]
	return info, ok
}
