package datastar_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/andrioid/gastro/datastar"
)

func TestClientPattern_IsGETOfContentHashedPath(t *testing.T) {
	if !regexp.MustCompile(`^/_gastro/datastar/[0-9a-f]{16}\.js$`).MatchString(datastar.ClientPath) {
		t.Fatalf("ClientPath = %q, want /_gastro/datastar/<sha256-prefix>.js", datastar.ClientPath)
	}
	if want := "GET " + datastar.ClientPath; datastar.ClientPattern != want {
		t.Errorf("ClientPattern = %q, want %q", datastar.ClientPattern, want)
	}
}

func TestClientHandler_ServesEmbeddedClientImmutably(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle(datastar.ClientPattern, datastar.ClientHandler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + datastar.ClientPath)
	if err != nil {
		t.Fatalf("GET client: %v", err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", got)
	}

	onDisk, err := os.ReadFile("client/datastar.js")
	if err != nil {
		t.Fatalf("read client/datastar.js: %v", err)
	}
	if !bytes.Equal(body, onDisk) {
		t.Fatal("served bytes differ from client/datastar.js")
	}
	sum := sha256.Sum256(body)
	if !strings.Contains(datastar.ClientPath, hex.EncodeToString(sum[:])[:16]) {
		t.Errorf("ClientPath %q does not carry the served bytes' sha256 prefix", datastar.ClientPath)
	}

	post, err := http.Post(srv.URL+datastar.ClientPath, "text/plain", nil)
	if err != nil {
		t.Fatalf("POST client: %v", err)
	}
	if err := post.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", post.StatusCode)
	}
}

func TestClientHandler_HonoursConditionalRequests(t *testing.T) {
	first := httptest.NewRecorder()
	datastar.ClientHandler().ServeHTTP(first, httptest.NewRequest(http.MethodGet, datastar.ClientPath, nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on client response")
	}

	r := httptest.NewRequest(http.MethodGet, datastar.ClientPath, nil)
	r.Header.Set("If-None-Match", etag)
	again := httptest.NewRecorder()
	datastar.ClientHandler().ServeHTTP(again, r)
	if again.Code != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", again.Code)
	}
}

func TestClient_IsDatastarV1MatchingClientVersion(t *testing.T) {
	js, err := os.ReadFile("client/datastar.js")
	if err != nil {
		t.Fatalf("read client: %v", err)
	}
	if !strings.HasPrefix(datastar.ClientVersion, "1.") {
		t.Errorf("ClientVersion = %q, want a v1 protocol client", datastar.ClientVersion)
	}
	if want := "// Datastar v" + datastar.ClientVersion + "\n"; !bytes.HasPrefix(js, []byte(want)) {
		t.Errorf("embedded client header does not match ClientVersion %q", datastar.ClientVersion)
	}
	if _, err := os.Stat("client/LICENSE.md"); err != nil {
		t.Errorf("client license missing: %v", err)
	}
}

func TestFuncs_DatastarScriptRendersClientTag(t *testing.T) {
	tmpl := template.Must(template.New("layout").Funcs(datastar.Funcs()).Parse(`<head>{{ datastarScript }}</head>`))
	var out strings.Builder
	if err := tmpl.Execute(&out, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := `<head><script type="module" src="` + datastar.ClientPath + `"></script></head>`
	if out.String() != want {
		t.Errorf("rendered:\ngot:  %s\nwant: %s", out.String(), want)
	}
}
