# Changelog

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
