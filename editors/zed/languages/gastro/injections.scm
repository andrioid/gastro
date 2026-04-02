; Inject Go into the frontmatter
((frontmatter) @content
 (#set! "language" "go"))

; Inject HTML into the template body
((template_body) @content
 (#set! "language" "html"))
