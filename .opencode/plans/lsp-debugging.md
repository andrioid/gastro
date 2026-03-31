# LSP Debugging Plan

## RESOLVED (2026-03-30)

**Root cause:** Go's build system ignores files whose names start with `_` or `.`.
The virtual files were named `__gastro_pages_index.go` — the leading `_` caused
Go (and therefore gopls) to completely skip them during package discovery.

**Fix applied:**
1. Renamed virtual file prefix from `__gastro_` to `gastro_` in `workspace.go:VirtualFilePath`
2. Added `func main() {}` to virtual files so gopls treats them as a buildable package

**Both integration tests now pass:**
- `TestLSP_FrontmatterDiagnostics` — 3 diagnostics returned in <1s
- `TestLSP_FrontmatterCompletions` — `fmt.Sprint*` completions returned in ~5s

**How it was found:** Running `go list .` in the shadow workspace showed "no Go files"
despite the `.go` file being present. Renaming the file to remove the `_` prefix made
`go build` find the package and report `undefined: postss` as expected.

---

## Previous Analysis (for reference)

### What was verified during investigation

1. gopls directly via LSP returns completions for `fmt.S` — **confirmed**
2. gopls directly via LSP returns diagnostics for `undefined: postss` — **confirmed** (3 diagnostics including unused variables)
3. gopls needs files at module root (not subdirectories) for diagnostics — **confirmed** and fixed
4. gopls stubs must not import `net/http` — **confirmed** and fixed (stubs now use only built-in types)
5. Virtual file import stripping works — **confirmed** (no duplicate imports in function body)
6. URI mapping from virtual file to `.gastro` file works — **confirmed** (test logs show `.gastro` URI)

### Hypotheses to investigate (in order of likelihood)

#### H1: Shadow workspace symlinks confuse gopls module resolution

The shadow workspace symlinks `go.mod`, `go.sum`, and source directories from the user's project. gopls may resolve symlinks differently than expected, or the symlinked `go.mod` pointing to a different directory may confuse its module cache.

**Test:** Write the virtual file's content to a completely standalone temp directory (no symlinks, fresh `go.mod`) and send it to gopls. If diagnostics appear, symlinks are the issue.

**Fix:** Copy `go.mod` instead of symlinking. Or create a minimal `go.mod` in the shadow workspace.

#### H2: gopls needs `workspace/didChangeWatchedFiles` notification

When a new `.go` file appears in the workspace (written by `UpdateFile`), gopls may not know about it until notified via `workspace/didChangeWatchedFiles`. The `didOpen` notification sends the content inline, but gopls might need the workspace-level notification to trigger full analysis.

**Test:** After writing the virtual file to disk, send a `workspace/didChangeWatchedFiles` notification for the virtual file URI. Then wait for diagnostics.

**Fix:** Add `workspace/didChangeWatchedFiles` notification to `syncToGopls`.

#### H3: gopls needs `textDocument/didSave` to trigger diagnostics

Some gopls configurations only run full analysis on save, not on open/change.

**Test:** Send `textDocument/didSave` after `didOpen` and check if diagnostics appear.

**Fix:** Send `didSave` after each `syncToGopls`.

#### H4: gopls is in "single file mode" because of workspace configuration

gopls 0.21.1 may default to a mode where it only provides limited analysis for files not part of the build graph. The virtual file is `package main` at the module root but isn't referenced by anything — gopls might treat it as an orphan.

**Test:** Add a `func main()` to the virtual file (or a build tag) so gopls considers it part of the build.

**Fix:** Add `func main() {}` to the virtual file template.

#### H5: Timing — gopls hasn't finished loading when diagnostics are checked

The integration test waits 15 seconds but gopls might need longer for workspace initialization.

**Test:** Increase timeout to 30+ seconds. Add `time.Sleep` between `didOpen` and the diagnostic check.

**Fix:** Wait for gopls's `window/logMessage` indicating workspace loading is complete before expecting diagnostics.

#### H6: gopls sends diagnostics for the virtual file URI, but `handleGoplsNotification` drops them

The `findGastroURIForVirtualURI` function does path comparison. If gopls normalizes the URI (resolves symlinks, adds/removes trailing slash), the comparison fails and diagnostics with actual errors are silently dropped, while the empty initial diagnostic passes through.

**Test:** Log ALL diagnostics received from gopls (with their URIs and counts) before any filtering. Check if non-empty diagnostics arrive for a different URI than expected.

**Fix:** Use path normalization (resolve symlinks, canonical paths) when comparing URIs.

## Implementation Steps

### Step 1: Add comprehensive logging to the diagnostic pipeline

In `handleGoplsNotification`:
- Log every `publishDiagnostics` received from gopls (URI + count + messages)
- Log the result of `findGastroURIForVirtualURI` (found or not found, with both URIs)
- Log when diagnostics are forwarded to the editor

In `syncToGopls`:
- Log the shadow workspace directory
- Log the virtual file path and content hash
- Log the URI sent to gopls via `didOpen`

### Step 2: Test H1 (symlinks)

Modify the integration test to:
1. After starting gastro-lsp, find the shadow workspace directory from stderr logs
2. List the contents of the shadow workspace
3. Check if `go.mod` is resolvable from the virtual file location
4. Try `go build` in the shadow workspace to see if the Go toolchain can analyze it

### Step 3: Test H2 (didChangeWatchedFiles)

Add to `syncToGopls` after writing the file:
```go
s.gopls.Notify("workspace/didChangeWatchedFiles", map[string]any{
    "changes": []map[string]any{
        {"uri": virtualURI, "type": 1}, // 1 = Created
    },
})
```

### Step 4: Test H4 (add func main)

In `workspace.go`, add `func main() {}` to the virtual file if it doesn't already have one. This ensures gopls considers the file part of a buildable package.

### Step 5: Test H6 (URI normalization)

In `findGastroURIForVirtualURI`, before comparison:
```go
// Resolve symlinks for comparison
resolvedVirtual, _ := filepath.EvalSymlinks(virtualPath)
resolvedExpected, _ := filepath.EvalSymlinks(ws.VirtualFilePath(relPath))
```

## Quick Test Script

Use this Python script to test hypotheses without modifying Go code:

```python
# Template: test gopls with a specific file setup
import subprocess, json, time, select, os, tempfile

GOPLS = "/Users/aos/.local/share/mise/installs/go-golang-org-x-tools-gopls/0.21.1/bin/gopls"
tmpdir = tempfile.mkdtemp()

# Set up the test workspace here
# ...

# Standard gopls LSP test harness
proc = subprocess.Popen([GOPLS, "serve"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=tmpdir)

def send(msg):
    body = json.dumps(msg).encode()
    proc.stdin.write(("Content-Length: %d\r\n\r\n" % len(body)).encode() + body)
    proc.stdin.flush()

def recv(timeout=10):
    end = time.time() + timeout
    while time.time() < end:
        r, _, _ = select.select([proc.stdout], [], [], 0.5)
        if not r: continue
        line = proc.stdout.readline()
        length = 0
        while line.strip():
            if line.startswith(b"Content-Length:"): length = int(line.split(b":")[1].strip())
            line = proc.stdout.readline()
        if length:
            return json.loads(proc.stdout.read(length))
    return None

# Initialize, open, collect diagnostics...
```

## Success Criteria

1. Integration test `TestLSP_FrontmatterDiagnostics` passes — gopls reports `undefined: postss` through gastro-lsp
2. Integration test `TestLSP_FrontmatterCompletions` passes — gopls returns `fmt.Sprint*` completions
3. In VS Code: typing `postss` (undefined) shows a red underline
4. In VS Code: typing `fmt.S` shows completion suggestions from gopls
