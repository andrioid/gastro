; Frontmatter delimiters
(frontmatter_delimiter) @punctuation.delimiter

; Template expressions
(template_expression) @embedded

; Component tags
(component_name) @type
(component_self_closing "<" @tag.delimiter)
(component_self_closing "/>" @tag.delimiter)
(component_open_tag "<" @tag.delimiter)
(component_open_tag ">" @tag.delimiter)
(component_close_tag "</" @tag.delimiter)
(component_close_tag ">" @tag.delimiter)

; Component props
(prop_name) @property
(prop_expression) @embedded
(prop_string) @string

; Slot
(slot_tag) @tag.builtin
