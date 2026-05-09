package codegen_test

import (
	"testing"

	"github.com/andrioid/gastro/internal/codegen"
)

func TestPageID_PlainPage(t *testing.T) {
	got := codegen.DerivePageID("pages/index.gastro")
	if got != "index" {
		t.Errorf("DerivePageID(pages/index.gastro) = %q, want %q", got, "index")
	}
}

func TestPageID_NestedPage(t *testing.T) {
	got := codegen.DerivePageID("pages/admin/index.gastro")
	if got != "admin_index" {
		t.Errorf("DerivePageID(pages/admin/index.gastro) = %q, want %q", got, "admin_index")
	}
}

func TestPageID_PlainComponent(t *testing.T) {
	got := codegen.DerivePageID("components/hero.gastro")
	if got != "hero" {
		t.Errorf("DerivePageID(components/hero.gastro) = %q, want %q", got, "hero")
	}
}

func TestPageID_DynamicSegment(t *testing.T) {
	got := codegen.DerivePageID("pages/blog/[slug].gastro")
	if got != "blog_slug" {
		t.Errorf("DerivePageID(pages/blog/[slug].gastro) = %q, want %q", got, "blog_slug")
	}
}

func TestPageID_HyphenAndDot(t *testing.T) {
	got := codegen.DerivePageID("components/icons/social-x.gastro")
	if got != "icons_social_x" {
		t.Errorf("DerivePageID(components/icons/social-x.gastro) = %q, want %q", got, "icons_social_x")
	}
}

func TestPageID_CollapseRepeatedUnderscores(t *testing.T) {
	// Two adjacent non-ident characters must collapse to a single
	// underscore so visually-distinct paths still produce stable
	// idents. The codegen-layer collision check (Phase 5) catches the
	// degenerate case where two different paths sanitise identically.
	got := codegen.DerivePageID("pages/foo--bar.gastro")
	if got != "foo_bar" {
		t.Errorf("DerivePageID(pages/foo--bar.gastro) = %q, want %q", got, "foo_bar")
	}
}

func TestPageID_TrimsLeadingTrailingUnderscores(t *testing.T) {
	got := codegen.DerivePageID("pages/[slug].gastro")
	if got != "slug" {
		t.Errorf("DerivePageID(pages/[slug].gastro) = %q, want %q", got, "slug")
	}
}

func TestPageID_NoKindPrefix(t *testing.T) {
	// Paths without a pages/ or components/ prefix should still
	// produce a valid id (e.g. tests, fragments, ad-hoc invocations).
	got := codegen.DerivePageID("widget.gastro")
	if got != "widget" {
		t.Errorf("DerivePageID(widget.gastro) = %q, want %q", got, "widget")
	}
}

func TestPageID_Collision(t *testing.T) {
	// Two semantically different paths sanitise to the same id. The
	// Phase 5 codegen-layer check is responsible for surfacing this
	// as an error; DerivePageID itself is content-blind.
	a := codegen.DerivePageID("pages/blog/[slug].gastro")
	b := codegen.DerivePageID("pages/blog/_slug_.gastro")
	if a != b {
		t.Errorf("expected collision between %q and %q, got %q vs %q",
			"pages/blog/[slug].gastro", "pages/blog/_slug_.gastro", a, b)
	}
}

func TestPageID_AllNonIdent(t *testing.T) {
	// Pathological input: every character is non-ident. Must still
	// return something usable rather than an empty string.
	got := codegen.DerivePageID("pages/---.gastro")
	if got != "anon" {
		t.Errorf("DerivePageID(pages/---.gastro) = %q, want %q", got, "anon")
	}
}
