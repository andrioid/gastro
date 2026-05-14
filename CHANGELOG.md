# Changelog

## [0.3.1](https://github.com/andrioid/gastro/compare/v0.3.0...v0.3.1) (2026-05-14)


### Features

* **compiler,examples:** nested-component request propagation + examples/i18n ([93ebeed](https://github.com/andrioid/gastro/commit/93ebeed4506d31a46acfc4e8aeb78668f14f06d0))
* **compiler,lsp:** WithRequestFuncs follow-ups — nested propagation + LSP completion ([e7bd6cd](https://github.com/andrioid/gastro/commit/e7bd6cdc436c00856c45a29b7bbee0416a738eb1))
* **compiler:** nested-component request propagation ([4effc91](https://github.com/andrioid/gastro/commit/4effc914ca1878ee9aae87ceb15124712364ad62))
* **compiler:** probe rejects binder helpers shadowing component names ([dffad1b](https://github.com/andrioid/gastro/commit/dffad1b48e4c5e11587309f8ce3e8dfe0b8dac97))
* **compiler:** Render.With(r) — request-aware component rendering ([a32db72](https://github.com/andrioid/gastro/commit/a32db722763df737719ee181162219805315205f))
* **compiler:** WithRequestFuncs runtime — request-aware template helpers ([0bffe3c](https://github.com/andrioid/gastro/commit/0bffe3cb9673d8f5867781bcf8845e52120a8326))
* **devloop:** `--watch GLOB` flag for dev/watch — extra files trigger restart ([999a29a](https://github.com/andrioid/gastro/commit/999a29a431e121545fc8b1eb9bf10eb74b98b7e8))
* **examples:** csrf + csp \u2014 the remaining two WithRequestFuncs consumers ([e54f00e](https://github.com/andrioid/gastro/commit/e54f00e4bcafafb23433c6fb84e178659a5bce63))
* **lsp:** discover WithRequestFuncs binders for template parse + completion ([f00a33d](https://github.com/andrioid/gastro/commit/f00a33d2ed934e4ef6da7ad456ad96aa9e80b7d5))
* **lsp:** hover, go-to-def, and non-literal binder diagnostic ([96db2cd](https://github.com/andrioid/gastro/commit/96db2cd91fb336495d8e4862dd95c66c0207b7a0))
* WithRequestFuncs — request-aware template helpers (PR 1/2) ([b89a67d](https://github.com/andrioid/gastro/commit/b89a67d377b3b8fa289473796a542ec87c3d83b8))
* WithRequestFuncs examples + --watch flag + docs (PR 2/2) ([d5cce1d](https://github.com/andrioid/gastro/commit/d5cce1dde1a6089c496fada71574843c6a1ee8ff))


### Documentation

* add ROADMAP.md and link it from README ([c4aab4c](https://github.com/andrioid/gastro/commit/c4aab4cdd82606829887f9a7a87f9b6043322c8a))
* add WithRequestFuncs section to docs/helpers.md ([7946375](https://github.com/andrioid/gastro/commit/794637523bf76d2f0ba6a0877434ebbf066474d8))
* full WithRequestFuncs documentation pass + DECISIONS entry ([d5eb147](https://github.com/andrioid/gastro/commit/d5eb14701702b16d217d7cae88327697de2334a2))
* refresh stale embed/CLI claims in README and design.md ([94eea87](https://github.com/andrioid/gastro/commit/94eea876f67aa667f1a6bd708c7c33666c72fb62))
* stale-claim fixes + ROADMAP.md ([fec81a1](https://github.com/andrioid/gastro/commit/fec81a178b6b9e1f42997f38a34c751aa7ab7d62))


### Miscellaneous

* gofmt drift in four pre-existing files ([6eed0ef](https://github.com/andrioid/gastro/commit/6eed0ef7e79e44da891ca2004c77b937e4532712))

## [0.3.0](https://github.com/andrioid/gastro/compare/v0.2.0...v0.3.0) (2026-05-10)


### ⚠ BREAKING CHANGES

* **cli:** cross-compile gastro for windows

### Bug Fixes

* **cli:** cross-compile gastro for windows ([c01b1d0](https://github.com/andrioid/gastro/commit/c01b1d0f02077e6d9aa4bf4b0b33e72abc8441ad))

## [0.2.0](https://github.com/andrioid/gastro/compare/v0.1.20...v0.2.0) (2026-05-10)


### ⚠ BREAKING CHANGES

* **codegen:** replace {{ markdown }} with //gastro:embed directive

### Features

* **audit:** add LSP shadow parity harness (cmd/auditshadow + mise task) ([e549346](https://github.com/andrioid/gastro/commit/e549346c11ede3feb67afb057ec575f415de3dce))
* **codegen:** export FindFrontmatterStart for LSP shadow source maps ([e927d24](https://github.com/andrioid/gastro/commit/e927d24da09e077540668e9eeb5a5dc9ebd8533e))
* **codegen:** hoist top-level frontmatter decls to package scope ([eeaca3c](https://github.com/andrioid/gastro/commit/eeaca3cf1bc41ebffa05741ffb8a6ca355a5775b))
* **codegen:** replace {{ markdown }} with //gastro:embed directive ([7450fc1](https://github.com/andrioid/gastro/commit/7450fc14034b7cae6e550c99ffc286397c707fb3))
* **examples/gastro:** migrate website to //gastro:embed + md helper ([7dc0314](https://github.com/andrioid/gastro/commit/7dc0314bc98ffa9253194f289c37f37eeb524156))
* **examples/gastro:** migrate website to Tailwind v4 ([daa3a59](https://github.com/andrioid/gastro/commit/daa3a59134b145e1965da813afcb7215b97c07f9))
* **examples:** mermaid diagram support in markdown renderer ([4611569](https://github.com/andrioid/gastro/commit/46115698e80341aab930678bb6d26b68e0bfa67b))
* **examples:** theme mermaid diagrams to match site palette ([510edad](https://github.com/andrioid/gastro/commit/510edade853fd7d38d00fe31d3a300b941370d00))
* **lsp:** //gastro:embed diagnostics, hover, and path completion ([74b9a5c](https://github.com/andrioid/gastro/commit/74b9a5c11aaee9dbc1a6422e508d8554b2507d13))
* **lsp:** add code-action quick-fix for //gastro:embed BadVarType ([30c5d44](https://github.com/andrioid/gastro/commit/30c5d448d0e54ce73f133387dfe3db0eb6567947))


### Bug Fixes

* **examples/gastro:** use `go tool gastro` in //go:generate ([2a2b9f3](https://github.com/andrioid/gastro/commit/2a2b9f3f34ea1b280ea2002cfbad4305fb1bc541))
* **examples:** tidy go.sum to add github.com/google/shlex ([9719fe8](https://github.com/andrioid/gastro/commit/9719fe86daeaac23c538e36509c65041e7e98aff))
* **lsp/server:** isolate GASTRO_PROJECT in package tests ([eea5107](https://github.com/andrioid/gastro/commit/eea5107bf7d35f0c6c466788fe7fe34762a4ae3d))
* **lsp/shadow:** mirror full Go module + propagate component imports ([e8c3ade](https://github.com/andrioid/gastro/commit/e8c3ade88b50b03aca0a2118888c40e8c40c407b))
* **lsp:** emit `_ = X` markers so queryVariableTypes resolves frontmatter var types ([253c985](https://github.com/andrioid/gastro/commit/253c985982bde05488f007e4cbf4663f4b79a0f7))
* **lsp:** re-trigger template diagnostics once gopls is ready ([063b132](https://github.com/andrioid/gastro/commit/063b1329c8b3fca795274b5375305d3dcf06483b))
* **lsp:** unify codegen/lsp dict-key validation, fix Children false-positive ([f9c4738](https://github.com/andrioid/gastro/commit/f9c47380c136e8510dc04318fbb05604f02291cf))


### Refactoring

* **lsp/shadow:** generate shadow .go files via codegen pipeline (R6) ([b914c68](https://github.com/andrioid/gastro/commit/b914c681897afcb2186b27a85256edfe400cbe7c))
* **lsp:** tighten cache semantics, scope-skip telemetry, dataMu discipline ([f3d6f64](https://github.com/andrioid/gastro/commit/f3d6f643ad53a04fb2cb5aad9ba603a6542070be))


### Documentation

* **contributing:** document LSP binary refresh + audit harness ([22f3a10](https://github.com/andrioid/gastro/commit/22f3a10d1dfd764b980da9f5a96f969277f1308c))
* **contributing:** refresh LSP section after gopls reliability fixes ([1955959](https://github.com/andrioid/gastro/commit/1955959b0c7e92a5ca85564e4db5fa102726e133))
* document //gastro:embed and the user-side markdown story ([15b7958](https://github.com/andrioid/gastro/commit/15b79587721c486e4bf30c5301dca5bc5b288c84))
* **history:** archive drop-markdown-directive plan as executed ([100b2ca](https://github.com/andrioid/gastro/commit/100b2ca24633fb68dd42c3423fafd5860360a660))
* **history:** archive five shipped plans ([99bbf31](https://github.com/andrioid/gastro/commit/99bbf316eeeea32b060eb8b6e503eb1b15bad6c6))
* **lsp:** close shadow audit; archive plan; record Q1–Q3 ([d91e3fb](https://github.com/andrioid/gastro/commit/d91e3fbaae6e20063f6188f843d4f64f2ef21db5))


### Miscellaneous

* fix gofmt drift in unrelated files ([a83bb0f](https://github.com/andrioid/gastro/commit/a83bb0f7257b86c44d1aa25485938b61eb63e23e))

## [0.1.20](https://github.com/andrioid/gastro/compare/v0.1.19...v0.1.20) (2026-05-03)


### Features

* **watch:** auto-detect Go module root for *.go watching ([cc177ed](https://github.com/andrioid/gastro/commit/cc177ed8a3241da67775f459d6b5f5a51df19a63))

## [0.1.19](https://github.com/andrioid/gastro/compare/v0.1.18...v0.1.19) (2026-05-03)


### Features

* **cli:** adopt `go tool gastro` as a first-class install method ([be7ec19](https://github.com/andrioid/gastro/commit/be7ec1943304d8952dfabf2b658b6816f0c6a8b2))
* **cli:** two stories — `gastro watch` + framework/library docs split ([ba9d6da](https://github.com/andrioid/gastro/commit/ba9d6da5790209d5b952460f80ea52f57d052564))

## [0.1.18](https://github.com/andrioid/gastro/compare/v0.1.17...v0.1.18) (2026-05-03)


### Features

* **cli:** add GASTRO_PROJECT env var to set project root ([5260a08](https://github.com/andrioid/gastro/commit/5260a0846dbb293eae9e9081c5b5a42457548a54))
* **lsp:** support nested gastro projects via structural heuristic ([15a1ed7](https://github.com/andrioid/gastro/commit/15a1ed74dedfb749c3d534030ffc3e9cd0b619f1))

## [0.1.17](https://github.com/andrioid/gastro/compare/v0.1.16...v0.1.17) (2026-05-02)


### Features

* **analysis:** shared response-write/missing-return analyzer ([d06f740](https://github.com/andrioid/gastro/commit/d06f740e4a01bc81209cc5f9825d948cb5f7b5e9))
* **codegen:** Track B page model \u2014 ambient (w, r) + conditional render ([5b7d635](https://github.com/andrioid/gastro/commit/5b7d635d684e7a281784742c381e6ef03922e54d))
* **codegen:** Wave 3 — typed children plumbing (A5) ([e64b553](https://github.com/andrioid/gastro/commit/e64b55354b6f9548a4c1ba67ed7fdaa38fa86d94))
* **compiler:** Wave 1 — component name collisions (A7) + WithDevMode (B2) ([133890e](https://github.com/andrioid/gastro/commit/133890ebbf8905a483db1afaced6914a255c8a43))
* **examples:** migrate to Track B (ambient w, r) + SSE single-file demo ([a9d08b2](https://github.com/andrioid/gastro/commit/a9d08b22bf39c4d288675c1b846816da9324239a))
* **router:** drop GET-only auto-routes for Track B ([0691c3f](https://github.com/andrioid/gastro/commit/0691c3f239c9c51a6ee82bf883606f27fde10866))
* **router:** Wave 4 — WithMiddleware (C2) + WithErrorHandler (C4) ([fe04a90](https://github.com/andrioid/gastro/commit/fe04a9099d39b19504881a3052f37d7d40606f10))
* **runtime:** gastroWriter + body-write tracking for Track B ([452e035](https://github.com/andrioid/gastro/commit/452e0353e84ce805814f9481b588c7da1bc2491f))


### Bug Fixes

* **compiler:** pipe generated .go files through go/format.Source ([a98dd21](https://github.com/andrioid/gastro/commit/a98dd2113e6fc1fa7d120ed9e0a689b60ce7078c))
* **examples:** add transitive chroma/goldmark deps to go.sum ([1c229b4](https://github.com/andrioid/gastro/commit/1c229b464926c1005befdb0e63fa0c434bf507ea))


### Documentation

* add Track B migration guide for downstream adopters ([bc4c5db](https://github.com/andrioid/gastro/commit/bc4c5db3a1d1c643843c168b487960548c3c7420))
* **components:** multi-line dict syntax + pre-render-in-frontmatter notes ([2c828de](https://github.com/andrioid/gastro/commit/2c828debcfd692f57a0bd9410de5db5b72df1a26))
* **contributing:** add deprecation-policy paragraph ([0bfd556](https://github.com/andrioid/gastro/commit/0bfd5566eb0b1ad8ef7629de7edb97dd65ce8d6b))
* **decisions:** record Track B \u2014 page model v2 ([aeb1546](https://github.com/andrioid/gastro/commit/aeb1546c5ef603e0c7810574c6269849cae7a1d8))
* **plan:** add frictions plan and mode-split companion report ([007ae5f](https://github.com/andrioid/gastro/commit/007ae5fe653e677885650c31e3f31620b7cbcc1b))
* **plan:** archive frictions plan — Wave 5 closed (Q4 audit drops A2) ([68535ec](https://github.com/andrioid/gastro/commit/68535ec32cbbc9fe3ac45956f6630be7c88d9c9f))
* **plan:** mark Wave 1 shipped ([c8b8983](https://github.com/andrioid/gastro/commit/c8b898391253071c25537cfc9e225d292d01c06d))
* **plan:** mark Wave 3 (A5) shipped, formalize Wave 4 open questions ([0c9e0a8](https://github.com/andrioid/gastro/commit/0c9e0a86b2c3a5eba5ebaba7111b8b91a1fba266))
* **plan:** record Wave 4 commit SHA ([02c86c6](https://github.com/andrioid/gastro/commit/02c86c63b688784184b1040dad819c187f454b9a))
* rename migrating-to-track-b.md \u2192 pages-v2-migration.md ([27bbcce](https://github.com/andrioid/gastro/commit/27bbccea52956baf605efbbfe7aad34511e4a934))
* rewrite pages, sse, and design \u00a721 for Track B ([3c592fb](https://github.com/andrioid/gastro/commit/3c592fbcf6c66e15b2417e44575a3d985f3a9f2e))
* secondary touch-ups for Track B ([cea7342](https://github.com/andrioid/gastro/commit/cea7342ebf34a7158c95884b2576cd025c012dce))

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
