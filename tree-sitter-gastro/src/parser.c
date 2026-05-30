#include "tree_sitter/parser.h"

#if defined(__GNUC__) || defined(__clang__)
#pragma GCC diagnostic ignored "-Wmissing-field-initializers"
#endif

#define LANGUAGE_VERSION 14
#define STATE_COUNT 27
#define LARGE_STATE_COUNT 2
#define SYMBOL_COUNT 25
#define ALIAS_COUNT 0
#define TOKEN_COUNT 14
#define EXTERNAL_TOKEN_COUNT 0
#define FIELD_COUNT 0
#define MAX_ALIAS_SEQUENCE_LENGTH 4
#define PRODUCTION_ID_COUNT 1

enum ts_symbol_identifiers {
  sym_frontmatter_delimiter = 1,
  sym__frontmatter_line = 2,
  sym_template_expression = 3,
  anon_sym_LT = 4,
  anon_sym_SLASH_GT = 5,
  anon_sym_GT = 6,
  anon_sym_LT_SLASH = 7,
  sym_component_name = 8,
  anon_sym_EQ = 9,
  sym_prop_name = 10,
  sym_prop_expression = 11,
  sym_prop_string = 12,
  sym_html_content = 13,
  sym_document = 14,
  sym_frontmatter_section = 15,
  sym_frontmatter = 16,
  sym_template_body = 17,
  sym_component_self_closing = 18,
  sym_component_open_tag = 19,
  sym_component_close_tag = 20,
  sym_component_prop = 21,
  aux_sym_frontmatter_repeat1 = 22,
  aux_sym_template_body_repeat1 = 23,
  aux_sym_component_self_closing_repeat1 = 24,
};

static const char * const ts_symbol_names[] = {
  [ts_builtin_sym_end] = "end",
  [sym_frontmatter_delimiter] = "frontmatter_delimiter",
  [sym__frontmatter_line] = "_frontmatter_line",
  [sym_template_expression] = "template_expression",
  [anon_sym_LT] = "<",
  [anon_sym_SLASH_GT] = "/>",
  [anon_sym_GT] = ">",
  [anon_sym_LT_SLASH] = "</",
  [sym_component_name] = "component_name",
  [anon_sym_EQ] = "=",
  [sym_prop_name] = "prop_name",
  [sym_prop_expression] = "prop_expression",
  [sym_prop_string] = "prop_string",
  [sym_html_content] = "html_content",
  [sym_document] = "document",
  [sym_frontmatter_section] = "frontmatter_section",
  [sym_frontmatter] = "frontmatter",
  [sym_template_body] = "template_body",
  [sym_component_self_closing] = "component_self_closing",
  [sym_component_open_tag] = "component_open_tag",
  [sym_component_close_tag] = "component_close_tag",
  [sym_component_prop] = "component_prop",
  [aux_sym_frontmatter_repeat1] = "frontmatter_repeat1",
  [aux_sym_template_body_repeat1] = "template_body_repeat1",
  [aux_sym_component_self_closing_repeat1] = "component_self_closing_repeat1",
};

static const TSSymbol ts_symbol_map[] = {
  [ts_builtin_sym_end] = ts_builtin_sym_end,
  [sym_frontmatter_delimiter] = sym_frontmatter_delimiter,
  [sym__frontmatter_line] = sym__frontmatter_line,
  [sym_template_expression] = sym_template_expression,
  [anon_sym_LT] = anon_sym_LT,
  [anon_sym_SLASH_GT] = anon_sym_SLASH_GT,
  [anon_sym_GT] = anon_sym_GT,
  [anon_sym_LT_SLASH] = anon_sym_LT_SLASH,
  [sym_component_name] = sym_component_name,
  [anon_sym_EQ] = anon_sym_EQ,
  [sym_prop_name] = sym_prop_name,
  [sym_prop_expression] = sym_prop_expression,
  [sym_prop_string] = sym_prop_string,
  [sym_html_content] = sym_html_content,
  [sym_document] = sym_document,
  [sym_frontmatter_section] = sym_frontmatter_section,
  [sym_frontmatter] = sym_frontmatter,
  [sym_template_body] = sym_template_body,
  [sym_component_self_closing] = sym_component_self_closing,
  [sym_component_open_tag] = sym_component_open_tag,
  [sym_component_close_tag] = sym_component_close_tag,
  [sym_component_prop] = sym_component_prop,
  [aux_sym_frontmatter_repeat1] = aux_sym_frontmatter_repeat1,
  [aux_sym_template_body_repeat1] = aux_sym_template_body_repeat1,
  [aux_sym_component_self_closing_repeat1] = aux_sym_component_self_closing_repeat1,
};

static const TSSymbolMetadata ts_symbol_metadata[] = {
  [ts_builtin_sym_end] = {
    .visible = false,
    .named = true,
  },
  [sym_frontmatter_delimiter] = {
    .visible = true,
    .named = true,
  },
  [sym__frontmatter_line] = {
    .visible = false,
    .named = true,
  },
  [sym_template_expression] = {
    .visible = true,
    .named = true,
  },
  [anon_sym_LT] = {
    .visible = true,
    .named = false,
  },
  [anon_sym_SLASH_GT] = {
    .visible = true,
    .named = false,
  },
  [anon_sym_GT] = {
    .visible = true,
    .named = false,
  },
  [anon_sym_LT_SLASH] = {
    .visible = true,
    .named = false,
  },
  [sym_component_name] = {
    .visible = true,
    .named = true,
  },
  [anon_sym_EQ] = {
    .visible = true,
    .named = false,
  },
  [sym_prop_name] = {
    .visible = true,
    .named = true,
  },
  [sym_prop_expression] = {
    .visible = true,
    .named = true,
  },
  [sym_prop_string] = {
    .visible = true,
    .named = true,
  },
  [sym_html_content] = {
    .visible = true,
    .named = true,
  },
  [sym_document] = {
    .visible = true,
    .named = true,
  },
  [sym_frontmatter_section] = {
    .visible = true,
    .named = true,
  },
  [sym_frontmatter] = {
    .visible = true,
    .named = true,
  },
  [sym_template_body] = {
    .visible = true,
    .named = true,
  },
  [sym_component_self_closing] = {
    .visible = true,
    .named = true,
  },
  [sym_component_open_tag] = {
    .visible = true,
    .named = true,
  },
  [sym_component_close_tag] = {
    .visible = true,
    .named = true,
  },
  [sym_component_prop] = {
    .visible = true,
    .named = true,
  },
  [aux_sym_frontmatter_repeat1] = {
    .visible = false,
    .named = false,
  },
  [aux_sym_template_body_repeat1] = {
    .visible = false,
    .named = false,
  },
  [aux_sym_component_self_closing_repeat1] = {
    .visible = false,
    .named = false,
  },
};

static const TSSymbol ts_alias_sequences[PRODUCTION_ID_COUNT][MAX_ALIAS_SEQUENCE_LENGTH] = {
  [0] = {0},
};

static const uint16_t ts_non_terminal_alias_map[] = {
  0,
};

static const TSStateId ts_primary_state_ids[STATE_COUNT] = {
  [0] = 0,
  [1] = 1,
  [2] = 2,
  [3] = 3,
  [4] = 4,
  [5] = 5,
  [6] = 6,
  [7] = 7,
  [8] = 8,
  [9] = 9,
  [10] = 10,
  [11] = 11,
  [12] = 12,
  [13] = 13,
  [14] = 14,
  [15] = 15,
  [16] = 16,
  [17] = 17,
  [18] = 18,
  [19] = 19,
  [20] = 20,
  [21] = 21,
  [22] = 22,
  [23] = 23,
  [24] = 24,
  [25] = 25,
  [26] = 26,
};

static bool ts_lex(TSLexer *lexer, TSStateId state) {
  START_LEXER();
  eof = lexer->eof(lexer);
  switch (state) {
    case 0:
      if (eof) ADVANCE(20);
      if (lookahead == '"') ADVANCE(8);
      if (lookahead == '-') ADVANCE(10);
      if (lookahead == '/') ADVANCE(11);
      if (lookahead == '<') ADVANCE(25);
      if (lookahead == '=') ADVANCE(32);
      if (lookahead == '>') ADVANCE(28);
      if (lookahead == '{') ADVANCE(12);
      if (('\t' <= lookahead && lookahead <= '\r') ||
          lookahead == ' ') SKIP(0);
      if (('A' <= lookahead && lookahead <= 'Z')) ADVANCE(31);
      if (('a' <= lookahead && lookahead <= 'z')) ADVANCE(33);
      END_STATE();
    case 1:
      if (lookahead == '\n') ADVANCE(21);
      END_STATE();
    case 2:
      if (lookahead == '\n') ADVANCE(21);
      if (lookahead != 0) ADVANCE(6);
      END_STATE();
    case 3:
      if (lookahead == '\n') ADVANCE(23);
      if (lookahead == '-') ADVANCE(4);
      if (('\t' <= lookahead && lookahead <= '\r') ||
          lookahead == ' ') ADVANCE(3);
      if (lookahead != 0) ADVANCE(6);
      END_STATE();
    case 4:
      if (lookahead == '\n') ADVANCE(22);
      if (lookahead == '-') ADVANCE(5);
      if (lookahead != 0) ADVANCE(6);
      END_STATE();
    case 5:
      if (lookahead == '\n') ADVANCE(22);
      if (lookahead == '-') ADVANCE(2);
      if (lookahead != 0) ADVANCE(6);
      END_STATE();
    case 6:
      if (lookahead == '\n') ADVANCE(22);
      if (lookahead != 0) ADVANCE(6);
      END_STATE();
    case 7:
      if (lookahead == '"') ADVANCE(8);
      if (lookahead == '/') ADVANCE(11);
      if (lookahead == '>') ADVANCE(28);
      if (lookahead == '{') ADVANCE(18);
      if (('\t' <= lookahead && lookahead <= '\r') ||
          lookahead == ' ') SKIP(7);
      if (('A' <= lookahead && lookahead <= 'Z') ||
          ('a' <= lookahead && lookahead <= 'z')) ADVANCE(33);
      END_STATE();
    case 8:
      if (lookahead == '"') ADVANCE(36);
      if (lookahead != 0) ADVANCE(8);
      END_STATE();
    case 9:
      if (lookahead == '-') ADVANCE(1);
      END_STATE();
    case 10:
      if (lookahead == '-') ADVANCE(9);
      END_STATE();
    case 11:
      if (lookahead == '>') ADVANCE(27);
      END_STATE();
    case 12:
      if (lookahead == '{') ADVANCE(13);
      if (lookahead != 0 &&
          lookahead != '}') ADVANCE(14);
      END_STATE();
    case 13:
      if (lookahead == '}') ADVANCE(35);
      if (lookahead != 0) ADVANCE(13);
      END_STATE();
    case 14:
      if (lookahead == '}') ADVANCE(34);
      if (lookahead != 0) ADVANCE(14);
      END_STATE();
    case 15:
      if (lookahead == '}') ADVANCE(24);
      END_STATE();
    case 16:
      if (lookahead == '}') ADVANCE(15);
      if (lookahead != 0) ADVANCE(16);
      END_STATE();
    case 17:
      if (('\t' <= lookahead && lookahead <= '\r') ||
          lookahead == ' ') SKIP(17);
      if (('A' <= lookahead && lookahead <= 'Z')) ADVANCE(31);
      END_STATE();
    case 18:
      if (lookahead != 0 &&
          lookahead != '}') ADVANCE(14);
      END_STATE();
    case 19:
      if (eof) ADVANCE(20);
      if (lookahead == '<') ADVANCE(26);
      if (lookahead == '{') ADVANCE(38);
      if (('\t' <= lookahead && lookahead <= '\r') ||
          lookahead == ' ') ADVANCE(37);
      if (lookahead != 0) ADVANCE(39);
      END_STATE();
    case 20:
      ACCEPT_TOKEN(ts_builtin_sym_end);
      END_STATE();
    case 21:
      ACCEPT_TOKEN(sym_frontmatter_delimiter);
      END_STATE();
    case 22:
      ACCEPT_TOKEN(sym__frontmatter_line);
      END_STATE();
    case 23:
      ACCEPT_TOKEN(sym__frontmatter_line);
      if (lookahead == '\n') ADVANCE(23);
      if (lookahead == '-') ADVANCE(4);
      if (('\t' <= lookahead && lookahead <= '\r') ||
          lookahead == ' ') ADVANCE(3);
      if (lookahead != 0) ADVANCE(6);
      END_STATE();
    case 24:
      ACCEPT_TOKEN(sym_template_expression);
      END_STATE();
    case 25:
      ACCEPT_TOKEN(anon_sym_LT);
      if (lookahead == '/') ADVANCE(29);
      END_STATE();
    case 26:
      ACCEPT_TOKEN(anon_sym_LT);
      if (lookahead == '/') ADVANCE(30);
      if (lookahead == '!' ||
          ('a' <= lookahead && lookahead <= 'z')) ADVANCE(39);
      END_STATE();
    case 27:
      ACCEPT_TOKEN(anon_sym_SLASH_GT);
      END_STATE();
    case 28:
      ACCEPT_TOKEN(anon_sym_GT);
      END_STATE();
    case 29:
      ACCEPT_TOKEN(anon_sym_LT_SLASH);
      END_STATE();
    case 30:
      ACCEPT_TOKEN(anon_sym_LT_SLASH);
      if (lookahead != 0 &&
          lookahead != '<' &&
          lookahead != '{') ADVANCE(39);
      END_STATE();
    case 31:
      ACCEPT_TOKEN(sym_component_name);
      if (('0' <= lookahead && lookahead <= '9') ||
          ('A' <= lookahead && lookahead <= 'Z') ||
          ('a' <= lookahead && lookahead <= 'z')) ADVANCE(31);
      END_STATE();
    case 32:
      ACCEPT_TOKEN(anon_sym_EQ);
      END_STATE();
    case 33:
      ACCEPT_TOKEN(sym_prop_name);
      if (('0' <= lookahead && lookahead <= '9') ||
          ('A' <= lookahead && lookahead <= 'Z') ||
          ('a' <= lookahead && lookahead <= 'z')) ADVANCE(33);
      END_STATE();
    case 34:
      ACCEPT_TOKEN(sym_prop_expression);
      END_STATE();
    case 35:
      ACCEPT_TOKEN(sym_prop_expression);
      if (lookahead == '}') ADVANCE(24);
      END_STATE();
    case 36:
      ACCEPT_TOKEN(sym_prop_string);
      END_STATE();
    case 37:
      ACCEPT_TOKEN(sym_html_content);
      if (lookahead == '<') ADVANCE(26);
      if (lookahead == '{') ADVANCE(38);
      if (('\t' <= lookahead && lookahead <= '\r') ||
          lookahead == ' ') ADVANCE(37);
      if (lookahead != 0) ADVANCE(39);
      END_STATE();
    case 38:
      ACCEPT_TOKEN(sym_html_content);
      if (lookahead == '{') ADVANCE(16);
      END_STATE();
    case 39:
      ACCEPT_TOKEN(sym_html_content);
      if (lookahead != 0 &&
          lookahead != '<' &&
          lookahead != '{') ADVANCE(39);
      END_STATE();
    default:
      return false;
  }
}

static const TSLexMode ts_lex_modes[STATE_COUNT] = {
  [0] = {.lex_state = 0},
  [1] = {.lex_state = 0},
  [2] = {.lex_state = 19},
  [3] = {.lex_state = 19},
  [4] = {.lex_state = 19},
  [5] = {.lex_state = 19},
  [6] = {.lex_state = 19},
  [7] = {.lex_state = 7},
  [8] = {.lex_state = 19},
  [9] = {.lex_state = 19},
  [10] = {.lex_state = 7},
  [11] = {.lex_state = 19},
  [12] = {.lex_state = 19},
  [13] = {.lex_state = 19},
  [14] = {.lex_state = 7},
  [15] = {.lex_state = 3},
  [16] = {.lex_state = 3},
  [17] = {.lex_state = 3},
  [18] = {.lex_state = 7},
  [19] = {.lex_state = 7},
  [20] = {.lex_state = 0},
  [21] = {.lex_state = 0},
  [22] = {.lex_state = 0},
  [23] = {.lex_state = 0},
  [24] = {.lex_state = 0},
  [25] = {.lex_state = 17},
  [26] = {.lex_state = 17},
};

static const uint16_t ts_parse_table[LARGE_STATE_COUNT][SYMBOL_COUNT] = {
  [0] = {
    [ts_builtin_sym_end] = ACTIONS(1),
    [sym_frontmatter_delimiter] = ACTIONS(1),
    [sym_template_expression] = ACTIONS(1),
    [anon_sym_LT] = ACTIONS(1),
    [anon_sym_SLASH_GT] = ACTIONS(1),
    [anon_sym_GT] = ACTIONS(1),
    [anon_sym_LT_SLASH] = ACTIONS(1),
    [sym_component_name] = ACTIONS(1),
    [anon_sym_EQ] = ACTIONS(1),
    [sym_prop_name] = ACTIONS(1),
    [sym_prop_expression] = ACTIONS(1),
    [sym_prop_string] = ACTIONS(1),
  },
  [1] = {
    [sym_document] = STATE(21),
    [sym_frontmatter_section] = STATE(2),
    [sym_frontmatter_delimiter] = ACTIONS(3),
  },
};

static const uint16_t ts_small_parse_table[] = {
  [0] = 6,
    ACTIONS(5), 1,
      ts_builtin_sym_end,
    ACTIONS(9), 1,
      anon_sym_LT,
    ACTIONS(11), 1,
      anon_sym_LT_SLASH,
    STATE(23), 1,
      sym_template_body,
    ACTIONS(7), 2,
      sym_template_expression,
      sym_html_content,
    STATE(3), 4,
      sym_component_self_closing,
      sym_component_open_tag,
      sym_component_close_tag,
      aux_sym_template_body_repeat1,
  [23] = 5,
    ACTIONS(9), 1,
      anon_sym_LT,
    ACTIONS(11), 1,
      anon_sym_LT_SLASH,
    ACTIONS(13), 1,
      ts_builtin_sym_end,
    ACTIONS(15), 2,
      sym_template_expression,
      sym_html_content,
    STATE(4), 4,
      sym_component_self_closing,
      sym_component_open_tag,
      sym_component_close_tag,
      aux_sym_template_body_repeat1,
  [43] = 5,
    ACTIONS(17), 1,
      ts_builtin_sym_end,
    ACTIONS(22), 1,
      anon_sym_LT,
    ACTIONS(25), 1,
      anon_sym_LT_SLASH,
    ACTIONS(19), 2,
      sym_template_expression,
      sym_html_content,
    STATE(4), 4,
      sym_component_self_closing,
      sym_component_open_tag,
      sym_component_close_tag,
      aux_sym_template_body_repeat1,
  [63] = 2,
    ACTIONS(28), 1,
      ts_builtin_sym_end,
    ACTIONS(30), 4,
      sym_template_expression,
      anon_sym_LT,
      anon_sym_LT_SLASH,
      sym_html_content,
  [73] = 2,
    ACTIONS(32), 1,
      ts_builtin_sym_end,
    ACTIONS(34), 4,
      sym_template_expression,
      anon_sym_LT,
      anon_sym_LT_SLASH,
      sym_html_content,
  [83] = 3,
    ACTIONS(38), 1,
      sym_prop_name,
    ACTIONS(36), 2,
      anon_sym_SLASH_GT,
      anon_sym_GT,
    STATE(7), 2,
      sym_component_prop,
      aux_sym_component_self_closing_repeat1,
  [95] = 2,
    ACTIONS(41), 1,
      ts_builtin_sym_end,
    ACTIONS(43), 4,
      sym_template_expression,
      anon_sym_LT,
      anon_sym_LT_SLASH,
      sym_html_content,
  [105] = 2,
    ACTIONS(45), 1,
      ts_builtin_sym_end,
    ACTIONS(47), 4,
      sym_template_expression,
      anon_sym_LT,
      anon_sym_LT_SLASH,
      sym_html_content,
  [115] = 4,
    ACTIONS(49), 1,
      anon_sym_SLASH_GT,
    ACTIONS(51), 1,
      anon_sym_GT,
    ACTIONS(53), 1,
      sym_prop_name,
    STATE(7), 2,
      sym_component_prop,
      aux_sym_component_self_closing_repeat1,
  [129] = 2,
    ACTIONS(55), 1,
      ts_builtin_sym_end,
    ACTIONS(57), 4,
      sym_template_expression,
      anon_sym_LT,
      anon_sym_LT_SLASH,
      sym_html_content,
  [139] = 2,
    ACTIONS(59), 1,
      ts_builtin_sym_end,
    ACTIONS(61), 4,
      sym_template_expression,
      anon_sym_LT,
      anon_sym_LT_SLASH,
      sym_html_content,
  [149] = 2,
    ACTIONS(63), 1,
      ts_builtin_sym_end,
    ACTIONS(65), 4,
      sym_template_expression,
      anon_sym_LT,
      anon_sym_LT_SLASH,
      sym_html_content,
  [159] = 4,
    ACTIONS(53), 1,
      sym_prop_name,
    ACTIONS(67), 1,
      anon_sym_SLASH_GT,
    ACTIONS(69), 1,
      anon_sym_GT,
    STATE(10), 2,
      sym_component_prop,
      aux_sym_component_self_closing_repeat1,
  [173] = 4,
    ACTIONS(71), 1,
      sym_frontmatter_delimiter,
    ACTIONS(73), 1,
      sym__frontmatter_line,
    STATE(17), 1,
      aux_sym_frontmatter_repeat1,
    STATE(24), 1,
      sym_frontmatter,
  [186] = 3,
    ACTIONS(75), 1,
      sym_frontmatter_delimiter,
    ACTIONS(77), 1,
      sym__frontmatter_line,
    STATE(16), 1,
      aux_sym_frontmatter_repeat1,
  [196] = 3,
    ACTIONS(80), 1,
      sym_frontmatter_delimiter,
    ACTIONS(82), 1,
      sym__frontmatter_line,
    STATE(16), 1,
      aux_sym_frontmatter_repeat1,
  [206] = 1,
    ACTIONS(84), 3,
      anon_sym_SLASH_GT,
      anon_sym_GT,
      sym_prop_name,
  [212] = 1,
    ACTIONS(86), 2,
      sym_prop_expression,
      sym_prop_string,
  [217] = 1,
    ACTIONS(88), 1,
      anon_sym_GT,
  [221] = 1,
    ACTIONS(90), 1,
      ts_builtin_sym_end,
  [225] = 1,
    ACTIONS(92), 1,
      anon_sym_EQ,
  [229] = 1,
    ACTIONS(94), 1,
      ts_builtin_sym_end,
  [233] = 1,
    ACTIONS(96), 1,
      sym_frontmatter_delimiter,
  [237] = 1,
    ACTIONS(98), 1,
      sym_component_name,
  [241] = 1,
    ACTIONS(100), 1,
      sym_component_name,
};

static const uint32_t ts_small_parse_table_map[] = {
  [SMALL_STATE(2)] = 0,
  [SMALL_STATE(3)] = 23,
  [SMALL_STATE(4)] = 43,
  [SMALL_STATE(5)] = 63,
  [SMALL_STATE(6)] = 73,
  [SMALL_STATE(7)] = 83,
  [SMALL_STATE(8)] = 95,
  [SMALL_STATE(9)] = 105,
  [SMALL_STATE(10)] = 115,
  [SMALL_STATE(11)] = 129,
  [SMALL_STATE(12)] = 139,
  [SMALL_STATE(13)] = 149,
  [SMALL_STATE(14)] = 159,
  [SMALL_STATE(15)] = 173,
  [SMALL_STATE(16)] = 186,
  [SMALL_STATE(17)] = 196,
  [SMALL_STATE(18)] = 206,
  [SMALL_STATE(19)] = 212,
  [SMALL_STATE(20)] = 217,
  [SMALL_STATE(21)] = 221,
  [SMALL_STATE(22)] = 225,
  [SMALL_STATE(23)] = 229,
  [SMALL_STATE(24)] = 233,
  [SMALL_STATE(25)] = 237,
  [SMALL_STATE(26)] = 241,
};

static const TSParseActionEntry ts_parse_actions[] = {
  [0] = {.entry = {.count = 0, .reusable = false}},
  [1] = {.entry = {.count = 1, .reusable = false}}, RECOVER(),
  [3] = {.entry = {.count = 1, .reusable = true}}, SHIFT(15),
  [5] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_document, 1, 0, 0),
  [7] = {.entry = {.count = 1, .reusable = false}}, SHIFT(3),
  [9] = {.entry = {.count = 1, .reusable = false}}, SHIFT(26),
  [11] = {.entry = {.count = 1, .reusable = false}}, SHIFT(25),
  [13] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_template_body, 1, 0, 0),
  [15] = {.entry = {.count = 1, .reusable = false}}, SHIFT(4),
  [17] = {.entry = {.count = 1, .reusable = true}}, REDUCE(aux_sym_template_body_repeat1, 2, 0, 0),
  [19] = {.entry = {.count = 2, .reusable = false}}, REDUCE(aux_sym_template_body_repeat1, 2, 0, 0), SHIFT_REPEAT(4),
  [22] = {.entry = {.count = 2, .reusable = false}}, REDUCE(aux_sym_template_body_repeat1, 2, 0, 0), SHIFT_REPEAT(26),
  [25] = {.entry = {.count = 2, .reusable = false}}, REDUCE(aux_sym_template_body_repeat1, 2, 0, 0), SHIFT_REPEAT(25),
  [28] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_component_open_tag, 3, 0, 0),
  [30] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_component_open_tag, 3, 0, 0),
  [32] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_component_close_tag, 3, 0, 0),
  [34] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_component_close_tag, 3, 0, 0),
  [36] = {.entry = {.count = 1, .reusable = true}}, REDUCE(aux_sym_component_self_closing_repeat1, 2, 0, 0),
  [38] = {.entry = {.count = 2, .reusable = true}}, REDUCE(aux_sym_component_self_closing_repeat1, 2, 0, 0), SHIFT_REPEAT(22),
  [41] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_component_open_tag, 4, 0, 0),
  [43] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_component_open_tag, 4, 0, 0),
  [45] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_component_self_closing, 4, 0, 0),
  [47] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_component_self_closing, 4, 0, 0),
  [49] = {.entry = {.count = 1, .reusable = true}}, SHIFT(9),
  [51] = {.entry = {.count = 1, .reusable = true}}, SHIFT(8),
  [53] = {.entry = {.count = 1, .reusable = true}}, SHIFT(22),
  [55] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_component_self_closing, 3, 0, 0),
  [57] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_component_self_closing, 3, 0, 0),
  [59] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_frontmatter_section, 3, 0, 0),
  [61] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_frontmatter_section, 3, 0, 0),
  [63] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_frontmatter_section, 2, 0, 0),
  [65] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_frontmatter_section, 2, 0, 0),
  [67] = {.entry = {.count = 1, .reusable = true}}, SHIFT(11),
  [69] = {.entry = {.count = 1, .reusable = true}}, SHIFT(5),
  [71] = {.entry = {.count = 1, .reusable = false}}, SHIFT(13),
  [73] = {.entry = {.count = 1, .reusable = false}}, SHIFT(17),
  [75] = {.entry = {.count = 1, .reusable = false}}, REDUCE(aux_sym_frontmatter_repeat1, 2, 0, 0),
  [77] = {.entry = {.count = 2, .reusable = false}}, REDUCE(aux_sym_frontmatter_repeat1, 2, 0, 0), SHIFT_REPEAT(16),
  [80] = {.entry = {.count = 1, .reusable = false}}, REDUCE(sym_frontmatter, 1, 0, 0),
  [82] = {.entry = {.count = 1, .reusable = false}}, SHIFT(16),
  [84] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_component_prop, 3, 0, 0),
  [86] = {.entry = {.count = 1, .reusable = true}}, SHIFT(18),
  [88] = {.entry = {.count = 1, .reusable = true}}, SHIFT(6),
  [90] = {.entry = {.count = 1, .reusable = true}},  ACCEPT_INPUT(),
  [92] = {.entry = {.count = 1, .reusable = true}}, SHIFT(19),
  [94] = {.entry = {.count = 1, .reusable = true}}, REDUCE(sym_document, 2, 0, 0),
  [96] = {.entry = {.count = 1, .reusable = true}}, SHIFT(12),
  [98] = {.entry = {.count = 1, .reusable = true}}, SHIFT(20),
  [100] = {.entry = {.count = 1, .reusable = true}}, SHIFT(14),
};

#ifdef __cplusplus
extern "C" {
#endif
#ifdef TREE_SITTER_HIDE_SYMBOLS
#define TS_PUBLIC
#elif defined(_WIN32)
#define TS_PUBLIC __declspec(dllexport)
#else
#define TS_PUBLIC __attribute__((visibility("default")))
#endif

TS_PUBLIC const TSLanguage *tree_sitter_gastro(void) {
  static const TSLanguage language = {
    .version = LANGUAGE_VERSION,
    .symbol_count = SYMBOL_COUNT,
    .alias_count = ALIAS_COUNT,
    .token_count = TOKEN_COUNT,
    .external_token_count = EXTERNAL_TOKEN_COUNT,
    .state_count = STATE_COUNT,
    .large_state_count = LARGE_STATE_COUNT,
    .production_id_count = PRODUCTION_ID_COUNT,
    .field_count = FIELD_COUNT,
    .max_alias_sequence_length = MAX_ALIAS_SEQUENCE_LENGTH,
    .parse_table = &ts_parse_table[0][0],
    .small_parse_table = ts_small_parse_table,
    .small_parse_table_map = ts_small_parse_table_map,
    .parse_actions = ts_parse_actions,
    .symbol_names = ts_symbol_names,
    .symbol_metadata = ts_symbol_metadata,
    .public_symbol_map = ts_symbol_map,
    .alias_map = ts_non_terminal_alias_map,
    .alias_sequences = &ts_alias_sequences[0][0],
    .lex_modes = ts_lex_modes,
    .lex_fn = ts_lex,
    .primary_state_ids = ts_primary_state_ids,
  };
  return &language;
}
#ifdef __cplusplus
}
#endif
