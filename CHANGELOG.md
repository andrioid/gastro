# Changelog

## [0.1.16](https://github.com/andrioid/gastro/compare/v0.1.15...v0.1.16) (2026-05-01)


### Features

* **cli:** gastro list with --json, GASTRO_DEV_ROOT, pages/ optional, generate timing ([e0a2bc4](https://github.com/andrioid/gastro/commit/e0a2bc46d3b23f435d514b1602258a672877e464))


### Documentation

* add frictions.md backlog (distilled from git-pm audit) ([b2ae9bf](https://github.com/andrioid/gastro/commit/b2ae9bf964cd89233c7ff6e92b5a0f3ef044ae01))
* surface gastro list, GASTRO_DEV_ROOT, and optional pages/ in skill, README, and getting-started ([62a91d8](https://github.com/andrioid/gastro/commit/62a91d88c8ae5c00f1cb0178eaa1314fe1e258f8))

## [0.1.15](https://github.com/andrioid/gastro/compare/v0.1.14...v0.1.15) (2026-04-30)


### Features

* **funcs:** add has, hasKey, set membership helpers ([8253b68](https://github.com/andrioid/gastro/commit/8253b686cf7aa9aaa25cd2c3fb739d1d7d62d2cd))


### Bug Fixes

* **codegen:** atomic active-router pointer + per-Router Render() ([31f1ce5](https://github.com/andrioid/gastro/commit/31f1ce57772880ba6f1c0804df380c1fba920c4f))
* **codegen:** build-time (dict ...) prop validation ([c9b615f](https://github.com/andrioid/gastro/commit/c9b615fc856689c8b97804bcf846aa018aad6962))
* **codegen:** support inline field comments in Props structs ([cd55683](https://github.com/andrioid/gastro/commit/cd5568337b8f2092f7c22d32bfd283604a2fce15))

## [0.1.14](https://github.com/andrioid/gastro/compare/v0.1.13...v0.1.14) (2026-04-26)


### Features

* **cli:** add 'gastro check' for CI drift detection ([c8fc130](https://github.com/andrioid/gastro/commit/c8fc130a799d8192875ebfa461523314f443f0e0))
* **codegen:** warn when ctx is used without gastro.Context() marker ([a72475b](https://github.com/andrioid/gastro/commit/a72475bbe92ba46e11ed2576489a51f1c5c0056c))
* handler-instance Router with WithDeps and WithOverride ([e5c25eb](https://github.com/andrioid/gastro/commit/e5c25eb3a7d0901084aa3fddcd8f93b23c013cbb))
* **pkg/gastro:** typed dependency injection for handlers ([caf4633](https://github.com/andrioid/gastro/commit/caf46335016bd75fa22b6531a24befcbf48e4186))


### Bug Fixes

* **cli:** derive module name from path basename in 'gastro new' ([cbccc57](https://github.com/andrioid/gastro/commit/cbccc5732bc8b49e82f389e3c36fb63c607f702e))
* **examples/gastro:** copy docs/ into Docker build context ([a93c96c](https://github.com/andrioid/gastro/commit/a93c96c6f0d4b2be89a1cb5814bbc6a401730696))


### Documentation

* **decisions:** record handler-instance Router refactor ([86639ad](https://github.com/andrioid/gastro/commit/86639adb60142093d110a2b6a1afcef2ee44fc7d))
* **design:** add evolution-from-original-API addendum (Section 21) ([e118a61](https://github.com/andrioid/gastro/commit/e118a61ac8e5b1551ce0ebe729d2b7b664039ca4))
* improve Render API discoverability ([611d632](https://github.com/andrioid/gastro/commit/611d632e03cddb95bac22ce8d8a6afe7d86de3ce))
* surface New(), WithDeps, WithOverride, gastro check across docs ([bb76e42](https://github.com/andrioid/gastro/commit/bb76e4201cbc7678e18c57f7c661ba113a3f93c6))


### Miscellaneous

* gitignore examples/gastro/app build artifact ([a37b88c](https://github.com/andrioid/gastro/commit/a37b88cb17dcaec46afef2366aa0a353ad6fd574))

## [0.1.13](https://github.com/andrioid/gastro/compare/v0.1.12...v0.1.13) (2026-04-18)


### Features

* **chromalexer:** add Chroma lexer for .gastro files ([0457b39](https://github.com/andrioid/gastro/commit/0457b393590a39d56a4cf1645c13ef25e3fd49db))
* compile-time {{ markdown }} directive + website docs consolidation ([4980dc2](https://github.com/andrioid/gastro/commit/4980dc20c87513081905ab28f84364a5b69cab35))
* compile-time {{ markdown }} directive for .gastro templates ([32158e5](https://github.com/andrioid/gastro/commit/32158e58d5e3e245c89f00c29395b00e812247c7))
* **dev:** watch out-of-tree markdown deps via compiler-reported paths ([3c7eb0a](https://github.com/andrioid/gastro/commit/3c7eb0a65e235c6ed52da1f0531d686ae49cca46))


### Bug Fixes

* **lsp:** complete & parse compile-time directives (wrap, raw, endraw, markdown) ([d5b02c7](https://github.com/andrioid/gastro/commit/d5b02c7f8d7aedce72f521dd0c60a7f9d4c58d36))


### Refactoring

* **codegen:** remove redundant dedup in ProcessMarkdownDirectives ([35204ed](https://github.com/andrioid/gastro/commit/35204edd4f9219aef585e21b6e6e608269fcb330))
* **codegen:** remove unused markdownPlaceholder constant ([323879e](https://github.com/andrioid/gastro/commit/323879ed5cff5d3efa4472e7961e17c487b07d8c))
* **examples/gastro:** consolidate docs into /docs/*.md, namespace guestbook examples ([1ec4b5c](https://github.com/andrioid/gastro/commit/1ec4b5c23e6493583fa8e9e6d8c70190e87ce381))


### Documentation

* **components:** label code fences as html instead of go ([58c35b4](https://github.com/andrioid/gastro/commit/58c35b4b7e347c0ecbf9b73c2cf41df3c8bbe3c4))
* use ```gastro fences for page and component examples ([f7ea1ad](https://github.com/andrioid/gastro/commit/f7ea1ad371456f50d6334afe1e260ba5853249ca))

## [0.1.12](https://github.com/andrioid/gastro/compare/v0.1.11...v0.1.12) (2026-04-06)


### Features

* detect missing Go, gopls, and gastro with actionable prompts ([19a3a95](https://github.com/andrioid/gastro/commit/19a3a95c2663de3907843d14d86f9d14526dc5a6))

## [0.1.11](https://github.com/andrioid/gastro/compare/v0.1.10...v0.1.11) (2026-04-06)


### Bug Fixes

* reject bare gastro.Props() on exported vars, replace link:vscode with install:vscode ([30f84e8](https://github.com/andrioid/gastro/commit/30f84e8773e8053c67784768d471d8f93db15476))


### Refactoring

* downgrade bare gastro.Props() from error to warning ([b4d05ed](https://github.com/andrioid/gastro/commit/b4d05ed7eb3939c78614f292862ee5db949f1333))

## [0.1.10](https://github.com/andrioid/gastro/compare/v0.1.9...v0.1.10) (2026-04-06)


### Features

* add snippet completions, version check, and VSCode README ([bc4bbea](https://github.com/andrioid/gastro/commit/bc4bbeadb9174a2ea9d99d3b3ceb883ead941bd5))

## [0.1.9](https://github.com/andrioid/gastro/compare/v0.1.8...v0.1.9) (2026-04-06)


### Features

* wire formatting into LSP server for editor support ([958a97f](https://github.com/andrioid/gastro/commit/958a97ffcbf061d51c0122d7d8af8a37378778b9))

## [0.1.8](https://github.com/andrioid/gastro/compare/v0.1.7...v0.1.8) (2026-04-06)


### Features

* add gastro fmt command for auto-formatting .gastro files ([6434182](https://github.com/andrioid/gastro/commit/6434182f786182026e71fa2c98699b82f18ead7e))


### Bug Fixes

* HoistTypeDeclarations falsely hoists type declarations inside backtick strings ([a6592db](https://github.com/andrioid/gastro/commit/a6592db1254bf500fee60e6636e2bc9fcf77bbc7))
* LSP auto-import discovers new components without restart ([c132475](https://github.com/andrioid/gastro/commit/c132475419fb1de2f4a745dd4b5137cfe484ba09))


### Refactoring

* replace custom utilities with Go stdlib equivalents ([a848038](https://github.com/andrioid/gastro/commit/a84803808b7ddc135e1ee2ad9648cfb58ed7f6f2))

## [0.1.7](https://github.com/andrioid/gastro/compare/v0.1.6...v0.1.7) (2026-04-05)


### Bug Fixes

* VS Code extension missing vscode-languageclient module ([8e8cfc0](https://github.com/andrioid/gastro/commit/8e8cfc07a013144fc8cd2025bfee83313af962bd))


### Refactoring

* scaffold uses file-based templates via embed.FS ([cb88ecc](https://github.com/andrioid/gastro/commit/cb88ecc4e158355eec359c544ed07df09afb126c))

## [0.1.6](https://github.com/andrioid/gastro/compare/v0.1.5...v0.1.6) (2026-04-05)


### Bug Fixes

* LSP deadlock, graceful degradation without gopls, and VS Code install prompt ([556015b](https://github.com/andrioid/gastro/commit/556015b3d59a12a53fa7a0bc3c2a78e38d60f765))


### Documentation

* rewrite getting started guide with mise install and first component ([4edf3f1](https://github.com/andrioid/gastro/commit/4edf3f125cdd4a89b08968d48b9a9d0a488975a6))

## [0.1.5](https://github.com/andrioid/gastro/compare/v0.1.4...v0.1.5) (2026-04-04)


### Documentation

* update Quick Start to use mise and gastro new ([b18fc9b](https://github.com/andrioid/gastro/commit/b18fc9ba60cfc7c529d6ae829147a7da1182411c))

## [0.1.4](https://github.com/andrioid/gastro/compare/v0.1.3...v0.1.4) (2026-04-04)


### Features

* auto-increment dev server port when default is in use ([048ea9d](https://github.com/andrioid/gastro/commit/048ea9dcde025fee203ccb51e0e2eef342437b95))
* include README.md with quickstart in scaffolded projects ([2a502d7](https://github.com/andrioid/gastro/commit/2a502d78148cc5d2b10c6391c11b510d1062569e))


### Bug Fixes

* handle empty static/ directory in scaffold and compiler ([b37e1d1](https://github.com/andrioid/gastro/commit/b37e1d1def4b882321ab19bea9ee7d02adbc2bec))

## [0.1.3](https://github.com/andrioid/gastro/compare/v0.1.2...v0.1.3) (2026-04-04)


### Bug Fixes

* create output directory for VS Code extension build ([2259c49](https://github.com/andrioid/gastro/commit/2259c49a42d0403f240a5b069a8d7a62899c6159))

## [0.1.2](https://github.com/andrioid/gastro/compare/v0.1.1...v0.1.2) (2026-04-04)


### Refactoring

* consolidate gastro-lsp into gastro lsp subcommand ([11324e3](https://github.com/andrioid/gastro/commit/11324e337e3a00233b5ee93c7e626726e3f3b9f3))

## [0.1.1](https://github.com/andrioid/gastro/compare/v0.1.0...v0.1.1) (2026-04-03)


### Features

* add compile-time {{ raw }}...{{ endraw }} blocks ([71a4be8](https://github.com/andrioid/gastro/commit/71a4be8385c80d3c0e0920c4788863e13cf98272))
* allow .gastro files without frontmatter ([fa660fa](https://github.com/andrioid/gastro/commit/fa660fad31682166d70b57c0f890fb1f9a9e988f))
* **examples:** add comparison page for Gastro vs Templ, gomponents, htmgo, and html/template ([3ae8c4a](https://github.com/andrioid/gastro/commit/3ae8c4a1838b57fba75c303dca2727851992bb9d))
* **examples:** guestbook demos, bug fixes, and security hardening ([2a9f26d](https://github.com/andrioid/gastro/commit/2a9f26d807a8f7d37ad99f9f63fa7482db4ed87a))


### Bug Fixes

* escape HTML in raw blocks and restore readActionKeyword dash support ([db51ae6](https://github.com/andrioid/gastro/commit/db51ae657ce9f8277b4ea57460c992c42308a692))


### Refactoring

* simplify raw blocks to always trim whitespace ([8f31236](https://github.com/andrioid/gastro/commit/8f31236b87e65c3fb239b73eb48dbf82ac4124bb))


### Miscellaneous

* **ci:** remove bootstrap-sha and release-as after v0.1.0 ([f62bd40](https://github.com/andrioid/gastro/commit/f62bd4044f833cc92e908293956edc259ac47d14))

## 0.1.0 (2026-04-03)


### Bug Fixes

* **ci:** create editors/vscode/bin/ directory before copying LSP binary ([294a5a9](https://github.com/andrioid/gastro/commit/294a5a98f4ebca156a94f5363d35729880029d16))
* **ci:** reset release-please to target v0.1.0 instead of v1.0.0 ([7e23e6f](https://github.com/andrioid/gastro/commit/7e23e6f9ce34a0bf1c4ef59063363500bb666033))
* **ci:** run vsce package from extension directory ([ba71bb5](https://github.com/andrioid/gastro/commit/ba71bb5c4e9480413e541cbef5653b1a2cd9897f))


### Miscellaneous

* **ci:** add release automation with release-please and conventional commits ([eb2fef5](https://github.com/andrioid/gastro/commit/eb2fef5c040045d20788d0042ff48e14541a2e85))
* **ci:** remove redundant cross-compile build from CI ([5423511](https://github.com/andrioid/gastro/commit/5423511f34a523906762604e1afec5772b5c1aae))
* **ci:** reset manifest for v0.1.0 re-release ([5429e00](https://github.com/andrioid/gastro/commit/5429e00d7791dcc6c23874b42a5fcda0f70ed380))
* **main:** release 0.1.0 ([c7cf44a](https://github.com/andrioid/gastro/commit/c7cf44a871cced25c61f32a1172836a9f14dc4fd))
* **main:** release 0.1.0 ([7d45df4](https://github.com/andrioid/gastro/commit/7d45df457ba0257a5f17ab16d450b3d4d04b197d))
* **main:** release 1.0.0 ([49673ac](https://github.com/andrioid/gastro/commit/49673ac2a7745d75af1773f9ea93cbe54fc06104))
* **main:** release 1.0.0 ([6f19733](https://github.com/andrioid/gastro/commit/6f19733eb2017edac7960486d605e271229c9b0c))
