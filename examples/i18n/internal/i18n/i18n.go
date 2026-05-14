// Package i18n is the example's tiny gettext-style internationalisation
// library. It is intentionally hand-rolled and minimal: ~150 LOC of plain
// Go that supports the three patterns the WithRequestFuncs example needs
// (simple translation, plurals, contexts) without pulling in a real PO
// library like gotext or go-i18n.
//
// Real production apps should use a battle-tested library. This package
// exists to show that WithRequestFuncs is library-agnostic — the
// adopter brings their own i18n primitives and ~15 LOC of glue.
//
// The translation flow:
//
//   1. Catalog.Load reads embedded *.po files and parses each into a
//      Localizer (one per locale).
//   2. Catalog.Middleware picks a Localizer for each request based on
//      the URL path (/da/..., /de/...), the gastro_locale cookie, or
//      the Accept-Language header — in that order. The chosen
//      Localizer is attached to the request context.
//   3. FromCtx pulls the Localizer back out at template time. The
//      WithRequestFuncs binder calls FromCtx, then returns a FuncMap
//      with method values closed over that Localizer (so {{ t "hi" }}
//      resolves against the right locale).
//
// Per the WithRequestFuncs contract (docs/helpers.md §"The binder
// contract"), FromCtx tolerates an empty context: a probe request at
// gastro.New() time passes through with no Localizer attached, and
// FromCtx returns a null localizer that echoes msgids back unchanged.
// This keeps the binder probe-safe without Gastro injecting sentinel
// values.
package i18n

import (
	"context"
	"embed"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// Localizer holds the translation entries for one locale.
type Localizer struct {
	Locale string

	// entries maps a plain msgid to its translation. Empty msgstr
	// entries are dropped at load time so T's fallback (return the
	// msgid) does the right thing.
	entries map[string]string

	// plurals maps msgid (the singular form, which gettext uses as
	// the dictionary key) to the slice of translations indexed by
	// plural form. Index 0 is singular, index 1 is plural; this
	// example doesn't implement CLDR plural rules — anything but
	// n==1 picks index 1. Production code should use a CLDR-aware
	// library like gotext.
	plurals map[string][]string

	// contexts maps "msgctxt\x04msgid" (the gettext disambiguation
	// convention) to msgstr.
	contexts map[string]string
}

// T returns the translation for msgid, or msgid itself when no
// translation is registered. This is the gettext convention: the
// source-language msgid serves as both the lookup key and the
// fallback display string.
func (l *Localizer) T(msgid string) string {
	if l == nil {
		return msgid
	}
	if s, ok := l.entries[msgid]; ok {
		return s
	}
	return msgid
}

// TN returns the appropriate plural form for n, falling back to the
// English singular/plural pair when the catalogue has no entry.
//
// Plural selection is intentionally simple (n==1 → singular, else
// plural). Real apps use CLDR-aware rules; this is a recipe example,
// not a translation library.
func (l *Localizer) TN(singular, plural string, n int) string {
	if l == nil {
		if n == 1 {
			return singular
		}
		return plural
	}
	if forms, ok := l.plurals[singular]; ok && len(forms) >= 2 {
		idx := 1
		if n == 1 {
			idx = 0
		}
		if forms[idx] != "" {
			return forms[idx]
		}
	}
	if n == 1 {
		return singular
	}
	return plural
}

// TC returns the contextual translation: same msgid can have different
// translations depending on context (e.g. "Open" the verb vs "Open" the
// adjective). Mirrors gettext's pgettext.
func (l *Localizer) TC(ctx, msgid string) string {
	if l == nil {
		return msgid
	}
	if s, ok := l.contexts[ctx+"\x04"+msgid]; ok && s != "" {
		return s
	}
	return msgid
}

// nullLocalizer returns a localizer that echoes msgids back unchanged.
// Used as the FromCtx return value when no Localizer has been attached
// to the context (probe-safe path).
func nullLocalizer() *Localizer { return nil }

// ctxKey is the unexported context-key type for the request-scoped
// Localizer. Unexported so adopters can't construct collisions.
type ctxKey struct{}

// FromCtx returns the Localizer attached to ctx by Catalog.Middleware,
// or a null localizer that echoes msgids when none is attached. The
// probe at gastro.New() invokes the binder with a context that has no
// Localizer; this graceful default keeps the probe safe.
func FromCtx(ctx context.Context) *Localizer {
	if l, ok := ctx.Value(ctxKey{}).(*Localizer); ok {
		return l
	}
	return nullLocalizer()
}

// Catalog holds the parsed Localizer set and the fallback locale.
type Catalog struct {
	locales  map[string]*Localizer
	order    []string // for deterministic Accept-Language matching
	fallback string
}

// Load parses every "<locale>.po" file from fs (under the supplied
// directory prefix) that matches one of the requested locales.
// Fallback is the locale used when the request's preferred locale
// isn't in the catalogue.
//
// Pass dir="i18n" for the canonical example layout. Tests pass a
// testdata-rooted prefix.
//
// Errors during parsing are returned eagerly — a broken PO file is an
// adopter bug, not a runtime fallback case.
func Load(fs embed.FS, dir string, locales []string, fallback string) (*Catalog, error) {
	c := &Catalog{
		locales:  make(map[string]*Localizer, len(locales)),
		order:    append([]string(nil), locales...),
		fallback: fallback,
	}
	for _, loc := range locales {
		raw, err := fs.ReadFile(filepath.Join(dir, loc+".po"))
		if err != nil {
			return nil, err
		}
		l, err := parsePO(string(raw))
		if err != nil {
			return nil, err
		}
		l.Locale = loc
		c.locales[loc] = l
	}
	return c, nil
}

// Locales returns the catalogue's known locale codes in registration
// order. Useful for lang-switcher components.
func (c *Catalog) Locales() []string { return append([]string(nil), c.order...) }

// Fallback returns the catalogue's fallback locale.
func (c *Catalog) Fallback() string { return c.fallback }

// Middleware attaches a Localizer to every incoming request. Locale is
// selected in this priority order:
//
//   1. URL path prefix: /da/..., /de/...
//   2. gastro_locale cookie
//   3. Accept-Language header (first matching locale, no quality-weight
//      parsing — keeps the example small)
//   4. The catalogue's fallback locale
//
// The request URL is not rewritten; downstream handlers see the same
// r.URL.Path that came in. This lets the gastro auto-routes match
// /index, /about etc. for every locale without locale-specific page
// files. (A real app might prefer the /[lang]/index.gastro pattern for
// SEO; this example trades that for simplicity.)
func (c *Catalog) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := c.pickLocale(r)
		ctx := context.WithValue(r.Context(), ctxKey{}, l)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (c *Catalog) pickLocale(r *http.Request) *Localizer {
	// 1. Path prefix.
	if loc, _ := splitLocaleFromPath(r.URL.Path); loc != "" {
		if l, ok := c.locales[loc]; ok {
			return l
		}
	}
	// 2. Cookie.
	if ck, err := r.Cookie("gastro_locale"); err == nil {
		if l, ok := c.locales[ck.Value]; ok {
			return l
		}
	}
	// 3. Accept-Language (simple first-match, no quality weights).
	if al := r.Header.Get("Accept-Language"); al != "" {
		for _, part := range strings.Split(al, ",") {
			tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
			tag = strings.SplitN(tag, "-", 2)[0]
			if l, ok := c.locales[tag]; ok {
				return l
			}
		}
	}
	// 4. Fallback.
	return c.locales[c.fallback]
}

// splitLocaleFromPath returns (locale, rest) when path starts with a
// "/xx/" segment, otherwise ("", path). Used both by the middleware and
// by lang-switcher components that want to swap the locale prefix.
func splitLocaleFromPath(path string) (string, string) {
	if !strings.HasPrefix(path, "/") {
		return "", path
	}
	rest := strings.TrimPrefix(path, "/")
	slash := strings.Index(rest, "/")
	candidate := rest
	if slash >= 0 {
		candidate = rest[:slash]
	}
	if len(candidate) >= 2 && len(candidate) <= 5 {
		// Locale candidates are short (en, da, de, en-GB, etc.).
		return candidate, "/" + rest
	}
	return "", path
}

// parsePO is a tiny line-based PO parser. It handles the subset gettext
// emits in well-behaved catalogues: msgid / msgstr / msgctxt /
// msgid_plural / msgstr[N] entries, comment lines (#), blank-line
// record separators, and adjacent-string continuation lines. Escapes
// are limited to \n, \t, \r, \\, and \".
//
// Anything more exotic (string concatenation across encodings, fuzzy
// flag handling, header-line metadata beyond the empty-msgid record)
// is out of scope; real adopters should reach for gotext or go-i18n.
func parsePO(src string) (*Localizer, error) {
	l := &Localizer{
		entries:  make(map[string]string),
		plurals:  make(map[string][]string),
		contexts: make(map[string]string),
	}

	type entry struct {
		msgctxt      string
		msgid        string
		msgidPlural  string
		msgstr       string
		msgstrPlural []string
	}
	var cur entry
	commit := func() {
		defer func() { cur = entry{} }()
		if cur.msgid == "" && cur.msgctxt == "" {
			return // header record (msgid "") or empty
		}
		switch {
		case cur.msgctxt != "":
			l.contexts[cur.msgctxt+"\x04"+cur.msgid] = cur.msgstr
		case cur.msgidPlural != "":
			forms := make([]string, len(cur.msgstrPlural))
			copy(forms, cur.msgstrPlural)
			l.plurals[cur.msgid] = forms
		case cur.msgstr != "":
			l.entries[cur.msgid] = cur.msgstr
		}
	}

	lines := strings.Split(src, "\n")
	var lastField *string
	var lastPluralIdx int = -1
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			commit()
			lastField = nil
			lastPluralIdx = -1
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		// Adjacent-string continuation: a quoted-string line attaches
		// to the most recent field.
		if strings.HasPrefix(line, `"`) {
			val, err := unquotePO(line)
			if err != nil {
				return nil, err
			}
			if lastField != nil {
				*lastField += val
			} else if lastPluralIdx >= 0 && lastPluralIdx < len(cur.msgstrPlural) {
				cur.msgstrPlural[lastPluralIdx] += val
			}
			continue
		}
		key, val, ok := splitPOLine(line)
		if !ok {
			continue
		}
		switch {
		case key == "msgctxt":
			cur.msgctxt = val
			lastField = &cur.msgctxt
			lastPluralIdx = -1
		case key == "msgid":
			cur.msgid = val
			lastField = &cur.msgid
			lastPluralIdx = -1
		case key == "msgid_plural":
			cur.msgidPlural = val
			lastField = &cur.msgidPlural
			lastPluralIdx = -1
		case key == "msgstr":
			cur.msgstr = val
			lastField = &cur.msgstr
			lastPluralIdx = -1
		case strings.HasPrefix(key, "msgstr["):
			idx, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(key, "msgstr["), "]"))
			if err != nil {
				continue
			}
			for len(cur.msgstrPlural) <= idx {
				cur.msgstrPlural = append(cur.msgstrPlural, "")
			}
			cur.msgstrPlural[idx] = val
			lastField = nil
			lastPluralIdx = idx
		}
	}
	commit() // tail record without trailing blank line
	return l, nil
}

// splitPOLine splits "key \"value\"" into (key, value, ok).
func splitPOLine(line string) (string, string, bool) {
	i := strings.IndexByte(line, ' ')
	if i < 0 {
		return "", "", false
	}
	key := line[:i]
	rest := strings.TrimSpace(line[i+1:])
	if !strings.HasPrefix(rest, `"`) {
		return "", "", false
	}
	val, err := unquotePO(rest)
	if err != nil {
		return "", "", false
	}
	return key, val, true
}

// unquotePO unescapes a "..." quoted string with a minimal escape set.
func unquotePO(s string) (string, error) {
	if !strings.HasPrefix(s, `"`) {
		return "", nil
	}
	// Strip trailing whitespace and any inline comment.
	end := strings.LastIndexByte(s, '"')
	if end <= 0 {
		return "", nil
	}
	inner := s[1:end]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c != '\\' || i+1 >= len(inner) {
			b.WriteByte(c)
			continue
		}
		i++
		switch inner[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		default:
			b.WriteByte(inner[i])
		}
	}
	return b.String(), nil
}
