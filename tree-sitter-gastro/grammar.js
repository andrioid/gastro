/// <reference types="tree-sitter-cli/dsl" />
// @ts-check

module.exports = grammar({
  name: "gastro",

  rules: {
    document: ($) =>
      seq(
        $.frontmatter_section,
        optional($.template_body),
      ),

    frontmatter_section: ($) =>
      seq(
        $.frontmatter_delimiter,
        optional($.frontmatter),
        $.frontmatter_delimiter,
      ),

    frontmatter_delimiter: (_) => /---\n/,

    // The frontmatter content — injected as Go via queries
    frontmatter: (_) => /[^]*?(?=\n---\n)/,

    // Everything after the closing --- delimiter
    template_body: ($) =>
      repeat1(
        choice(
          $.template_expression,
          $.component_self_closing,
          $.component_open_tag,
          $.component_close_tag,
          $.html_content,
        ),
      ),

    // {{ ... }} Go template expressions
    template_expression: (_) => /\{\{[^}]*\}\}/,

    // <PascalCase prop={expr} prop="literal" />
    component_self_closing: ($) =>
      seq(
        "<",
        $.component_name,
        repeat($.component_prop),
        "/>",
      ),

    // <PascalCase prop={expr}>
    component_open_tag: ($) =>
      seq(
        "<",
        $.component_name,
        repeat($.component_prop),
        ">",
      ),

    // </PascalCase>
    component_close_tag: ($) =>
      seq(
        "</",
        $.component_name,
        ">",
      ),

    // PascalCase identifier (starts with uppercase)
    component_name: (_) => /[A-Z][a-zA-Z0-9]*/,

    // PropName={.expr} or PropName="literal"
    component_prop: ($) =>
      seq(
        $.prop_name,
        "=",
        choice($.prop_expression, $.prop_string),
      ),

    prop_name: (_) => /[A-Za-z][A-Za-z0-9]*/,

    // {.expr}
    prop_expression: (_) => /\{[^}]+\}/,

    // "literal"
    prop_string: (_) => /"[^"]*"/,

    // Any other HTML content
    html_content: (_) => /[^<{]+|[<{]/,
  },
});
