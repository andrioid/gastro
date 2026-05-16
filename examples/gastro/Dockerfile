FROM golang:1.26-alpine AS build
WORKDIR /src

# git is needed by `go install` for module resolution.
RUN apk add --no-cache git

# Install gopls into /go/bin. The live-LSP demo on the homepage spawns
# `gastro lsp` at runtime, which in turn shells out to gopls for Go
# frontmatter analysis. Pinned to whatever @latest resolves to at
# build time; pin to a tag if reproducible builds matter to you.
RUN CGO_ENABLED=0 go install golang.org/x/tools/gopls@latest

# Copy the full gastro source (needed for the CLI and replace directive)
COPY go.mod go.sum* ./
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
# Markdown source referenced by {{ markdown "../../../../docs/*.md" }}
# directives in examples/gastro/pages/docs/*.gastro.
COPY docs/ docs/

# Build the gastro CLI
RUN CGO_ENABLED=0 go build -o /usr/local/bin/gastro ./cmd/gastro/

# Copy the website example into a path where the replace directive
# (replace github.com/andrioid/gastro => ../..) resolves to /src
COPY examples/gastro/ /src/examples/gastro/
WORKDIR /src/examples/gastro

# Generate Go code from .gastro files and build the binary
RUN gastro generate
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /gastro-website .

# -------------------------------------------------------------------
# Module cache stage: pre-download ONLY the deps the live-LSP demo
# temp project transitively requires (github.com/andrioid/gastro →
# github.com/google/shlex). The example's pages pull in chroma +
# goldmark and a long transitive tail; bundling that whole cache
# (~160MB) into the runtime is wasted because the embedded demo
# .gastro file doesn't import any of it.
#
# We build the minimal cache by running `go mod download` against a
# scratch module that requires only gastro. Its GOMODCACHE
# (~/go/pkg/mod) ends up containing exactly what gopls needs at
# runtime to analyse the demo file's shadow.
# -------------------------------------------------------------------
FROM golang:1.26-alpine AS modcache
RUN apk add --no-cache git
COPY --from=build /src /src
WORKDIR /tmp/demoproj
RUN printf 'module demoproj\n\ngo 1.26.1\n\nrequire github.com/andrioid/gastro v0.0.0\n\nreplace github.com/andrioid/gastro => /src\n' > go.mod
# `go mod download all` pulls every required module into GOMODCACHE,
# resolving shlex (and any future transitive deps gastro picks up)
# from the network. With the replace directive in place gastro
# itself isn't downloaded — only its transitive deps.
# The golang container's default GOMODCACHE is /go/pkg/mod.
RUN go mod download all

# -------------------------------------------------------------------
# Runtime stage.
# -------------------------------------------------------------------
FROM alpine:3
RUN adduser -D -u 1000 appuser

# Go toolchain. gopls shells out to `go list`, `go mod`, etc. for
# module graph queries; without /usr/local/go on disk gopls returns
# empty results for every analysis. We strip the parts that aren't
# needed at runtime (test suite, API checksums, misc) to keep the
# image smaller.
#
# Kept:
#   /usr/local/go/bin (16MB)  — the `go` and `gofmt` binaries
#   /usr/local/go/src (156MB) — stdlib source, needed by gopls for
#                                hover/completion on std types
#   /usr/local/go/pkg (61MB)  — pre-compiled stdlib; without this
#                                the first analysis pays a one-time
#                                ~5s recompile cost
#   /usr/local/go/lib (2MB)   — the time-zone DB + ICU data
# Dropped:
#   /usr/local/go/test (20MB) — Go's own test suite
#   /usr/local/go/api (9MB)   — versioning checksums
#   /usr/local/go/doc (0.4MB) — HTML docs
#   /usr/local/go/misc (0.1MB)— misc tooling
COPY --from=build /usr/local/go/bin /usr/local/go/bin
COPY --from=build /usr/local/go/src /usr/local/go/src
COPY --from=build /usr/local/go/pkg /usr/local/go/pkg
COPY --from=build /usr/local/go/lib /usr/local/go/lib
COPY --from=build /usr/local/go/go.env /usr/local/go/go.env
ENV PATH=/usr/local/go/bin:$PATH
ENV GOROOT=/usr/local/go

# /opt/gastro mirrors the gastro repo layout. The runtime temp
# project's go.mod has `replace github.com/andrioid/gastro =>
# /opt/gastro` so gopls can resolve the gastro module from on-disk
# source. Discovered while building Steps 1+2 — see
# tmp/lsp-demo-plan.md under 'Runtime project layout'.
COPY --from=build --chown=appuser:appuser /src/go.mod /opt/gastro/go.mod
COPY --from=build --chown=appuser:appuser /src/go.sum /opt/gastro/go.sum
COPY --from=build --chown=appuser:appuser /src/cmd /opt/gastro/cmd
COPY --from=build --chown=appuser:appuser /src/internal /opt/gastro/internal
COPY --from=build --chown=appuser:appuser /src/pkg /opt/gastro/pkg
# The example's go.sum is copied into the temp project at runtime
# (see lspdemo.writeTempProject) so `go run`-style invocations don't
# bail on missing module hashes. The minimal go.mod the temp
# project actually uses only requires gastro — having extra
# hashes is harmless.
COPY --from=build --chown=appuser:appuser /src/examples/gastro/go.mod /opt/gastro/examples/gastro/go.mod
COPY --from=build --chown=appuser:appuser /src/examples/gastro/go.sum /opt/gastro/examples/gastro/go.sum

# Minimal module cache: just gastro's transitive deps (shlex), built
# in the modcache stage from a scratch go.mod that requires only
# gastro. ~1MB instead of the ~160MB the example's full cache
# would be.
COPY --from=modcache --chown=appuser:appuser /go/pkg/mod /home/appuser/go/pkg/mod

# gastro CLI and gopls on PATH. The LSP subprocess invocation
# (`gastro lsp`) shells out to gopls so both must be available.
COPY --from=build /usr/local/bin/gastro /usr/local/bin/gastro
COPY --from=build /go/bin/gopls /usr/local/bin/gopls

# Tell lspdemo.resolveBootOptions where the gastro source lives and
# which binary to spawn. See main.go for the env var contract.
ENV GASTRO_SOURCE_ROOT=/opt/gastro
ENV GASTRO_LSP_CMD="/usr/local/bin/gastro lsp"
ENV GOPATH=/home/appuser/go

USER appuser
COPY --from=build /gastro-website /gastro-website
EXPOSE 4242
CMD ["/gastro-website"]
