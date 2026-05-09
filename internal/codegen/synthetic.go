package codegen

// SyntheticPropKind classifies a dict key that is recognised by the
// gastro runtime even though it is not a user-declared field on a
// component's Props struct. Keys of kind SyntheticChildren are accepted
// silently; keys of kind SyntheticDeprecatedChildren are accepted with
// a migration hint (they used to be the wire name and are still seen
// in older templates).
//
// Single source of truth for both the codegen-time validator
// (ValidateDictKeys) and the LSP-time diagnostic. If a third synthetic
// key ever ships, this is the only place that needs to change.
type SyntheticPropKind int

const (
	// SyntheticNone — the key is not a recognised synthetic.
	SyntheticNone SyntheticPropKind = iota

	// SyntheticChildren is the canonical "Children" key. Injected by
	// TransformTemplate for {{ wrap }} blocks and accepted as an
	// explicit dict key on components that render {{ .Children }}.
	SyntheticChildren

	// SyntheticDeprecatedChildren is the pre-A5 "__children" sentinel.
	// No longer recognised by the runtime; surfaced as a hint so users
	// migrating old hand-written dicts know what to change.
	SyntheticDeprecatedChildren
)

// SyntheticPropKey returns the kind of synthetic prop key for the given
// dict key name and a boolean indicating whether the name is synthetic
// at all. The boolean is redundant with `kind != SyntheticNone` but is
// kept for ergonomic call sites that want `if _, ok := ...; ok`.
func SyntheticPropKey(name string) (SyntheticPropKind, bool) {
	switch name {
	case "Children":
		return SyntheticChildren, true
	case "__children":
		return SyntheticDeprecatedChildren, true
	default:
		return SyntheticNone, false
	}
}
