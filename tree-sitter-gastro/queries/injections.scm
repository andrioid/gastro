; Inject Go into the frontmatter
((frontmatter) @injection.content
 (#set! injection.language "go"))

; Inject HTML into the template body
((template_body) @injection.content
 (#set! injection.language "html"))
