package codegen

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/andrioid/gastro/internal/parser"
)

// TransformTemplate transforms the template body, converting <Component /> tags
// into Go template function calls and <slot /> into {{ .Children }}.
func TransformTemplate(body string, uses []parser.UseDeclaration) (string, error) {
	knownComponents := make(map[string]bool, len(uses))
	for _, u := range uses {
		knownComponents[u.Name] = true
	}

	result := body

	// Replace <slot /> with {{ .Children }}
	result = replaceSlotTags(result)

	// Replace component tags (both self-closing and with children)
	var err error
	result, err = replaceComponents(result, knownComponents)
	if err != nil {
		return "", err
	}

	return result, nil
}

var slotRegex = regexp.MustCompile(`<slot\s*/>`)

func replaceSlotTags(body string) string {
	return slotRegex.ReplaceAllString(body, "{{ .Children }}")
}

// replaceComponents processes the template body, replacing component tags.
// Handles both self-closing <Name /> and open/close <Name>...</Name> tags.
// Uses iterative string scanning instead of regex to correctly handle nesting.
func replaceComponents(body string, known map[string]bool) (string, error) {
	// Keep processing until no more component tags are found.
	// Process innermost components first (self-closing), then outer wrappers.
	for {
		changed := false

		// First pass: replace self-closing <Name prop={x} />
		newBody, didChange, err := replaceSelfClosing(body, known)
		if err != nil {
			return "", err
		}
		if didChange {
			body = newBody
			changed = true
		}

		// Second pass: replace innermost <Name>...</Name> (no nested same-name tags)
		newBody, didChange, err = replaceWithChildren(body, known)
		if err != nil {
			return "", err
		}
		if didChange {
			body = newBody
			changed = true
		}

		if !changed {
			break
		}
	}

	return body, nil
}

// selfClosingTagRegex matches a self-closing component tag on a single logical
// unit. It requires the tag name to start with uppercase, and matches props
// up to the closing />. The key constraint: no > character in the props
// (which prevents matching across line boundaries into other tags).
var selfClosingTagRegex = regexp.MustCompile(`<([A-Z][a-zA-Z0-9]*)((?:\s+\w+=(?:\{[^}]*\}|"[^"]*"))*)\s*/>`)

func replaceSelfClosing(body string, known map[string]bool) (string, bool, error) {
	didChange := false
	var replaceErr error

	result := selfClosingTagRegex.ReplaceAllStringFunc(body, func(match string) string {
		if replaceErr != nil {
			return match
		}

		groups := selfClosingTagRegex.FindStringSubmatch(match)
		name := groups[1]
		propsStr := strings.TrimSpace(groups[2])

		if !known[name] {
			replaceErr = fmt.Errorf("unknown component <%s />: not imported via 'use'", name)
			return match
		}

		didChange = true
		dictCall := buildDictCall(propsStr)
		return fmt.Sprintf("{{ __gastro_%s (%s) }}", name, dictCall)
	})

	return result, didChange, replaceErr
}

// openTagRegex matches a component open tag: <Name props...>
// It matches the FIRST occurrence of a PascalCase open tag.
var openTagRegex = regexp.MustCompile(`<([A-Z][a-zA-Z0-9]*)((?:\s+\w+=(?:\{[^}]*\}|"[^"]*"))*)\s*>`)

func replaceWithChildren(body string, known map[string]bool) (string, bool, error) {
	loc := openTagRegex.FindStringIndex(body)
	if loc == nil {
		return body, false, nil
	}

	match := openTagRegex.FindStringSubmatch(body[loc[0]:loc[1]])
	name := match[1]
	propsStr := strings.TrimSpace(match[2])

	if !known[name] {
		if isPascalCase(name) {
			return "", false, fmt.Errorf("unknown component <%s>: not imported via 'use'", name)
		}
		return body, false, nil
	}

	// Find the matching closing tag </Name>
	closeTag := fmt.Sprintf("</%s>", name)
	closeIdx := strings.Index(body[loc[1]:], closeTag)
	if closeIdx == -1 {
		return "", false, fmt.Errorf("unclosed component tag <%s>", name)
	}
	closeIdx += loc[1]

	dictCall := buildDictCall(propsStr)
	childTemplateName := fmt.Sprintf("%s_children", strings.ToLower(name))

	replacement := fmt.Sprintf(
		`{{ __gastro_%s (%s "__children" (__gastro_render_children "%s" .)) }}`,
		name, dictCall, childTemplateName,
	)

	result := body[:loc[0]] + replacement + body[closeIdx+len(closeTag):]
	return result, true, nil
}

// propRegex matches Key={.expr} or Key="literal" patterns
var propRegex = regexp.MustCompile(`(\w+)=(?:\{([^}]+)\}|"([^"]*)")`)

// buildDictCall parses props and builds a Go template `dict` call.
// e.g. `Title={.Name} Urgent={.IsHot}` -> `dict "Title" .Name "Urgent" .IsHot`
func buildDictCall(propsStr string) string {
	if strings.TrimSpace(propsStr) == "" {
		return "dict"
	}

	matches := propRegex.FindAllStringSubmatch(propsStr, -1)
	if len(matches) == 0 {
		return "dict"
	}

	var parts []string
	parts = append(parts, "dict")
	for _, m := range matches {
		key := m[1]
		if m[2] != "" {
			// {.expr} form
			parts = append(parts, fmt.Sprintf("%q %s", key, m[2]))
		} else {
			// "literal" form
			parts = append(parts, fmt.Sprintf("%q %q", key, m[3]))
		}
	}

	return strings.Join(parts, " ")
}

func isPascalCase(s string) bool {
	if s == "" {
		return false
	}
	return unicode.IsUpper(rune(s[0]))
}
