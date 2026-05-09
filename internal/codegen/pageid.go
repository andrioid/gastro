package codegen

import (
	"strings"
)

// DerivePageID returns the per-page prefix used to namespace hoisted
// declarations. It strips the top-level dir (e.g. "pages/" or
// "components/"), replaces path separators and non-Go-ident characters
// with "_", and collapses repeated underscores so two visually-distinct
// paths cannot accidentally collapse to the same id mid-walk (we use
// the deterministic input string to derive collision warnings later).
//
//	pages/admin/index.gastro        → "admin_index"
//	components/hero.gastro          → "hero"
//	pages/blog/[slug].gastro        → "blog_slug"
//	components/icons/social-x.gastro → "icons_social_x"
//
// The caller composes the result with "__page_" or "__component_" to
// form the full mangled prefix. Two paths that sanitise to the same id
// (e.g. "blog/[slug]" vs "blog/_slug_") must be detected by the caller
// at codegen time — DerivePageID itself is intentionally lossy because
// otherwise the prefix would carry stray punctuation.
func DerivePageID(relativePath string) string {
	p := strings.TrimSpace(relativePath)
	p = strings.TrimSuffix(p, ".gastro")

	// Strip the top-level kind directory if present. The mangled prefix
	// already encodes "page" vs "component"; including "pages/" in the
	// id would just produce __page_pages_X which is noisy.
	for _, kind := range []string{"pages/", "components/"} {
		if strings.HasPrefix(p, kind) {
			p = p[len(kind):]
			break
		}
	}

	var b strings.Builder
	b.Grow(len(p))
	prevUnderscore := false
	for _, r := range p {
		if isIdentRune(r) && r != '_' {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		// Underscores from the source AND non-ident chars both fold
		// into a single underscore. This makes paths like
		// `blog/[slug]` and `blog/_slug_` deliberately collide so the
		// codegen-layer check (Phase 5) can flag them — better than
		// silently allowing two distinct files to map to different
		// idents whose only difference is punctuation.
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	id := strings.Trim(b.String(), "_")
	if id == "" {
		// Defensive: should never happen for any real .gastro path,
		// but if it does, use a sentinel rather than emit an empty
		// prefix that would create __page__Title (double underscore).
		return "anon"
	}
	return id
}

// isIdentRune reports whether r is a valid Go identifier character.
// Restricted to ASCII letters, digits, and underscore — extended Unicode
// idents are valid Go but produce noisy mangled names that are awkward
// to debug; users can rename the file if they want a cleaner prefix.
func isIdentRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_'
}
