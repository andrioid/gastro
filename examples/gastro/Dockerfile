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

FROM alpine:3
RUN adduser -D -u 1000 appuser

# /opt/gastro mirrors the gastro repo layout. The lspdemo package's
# Boot function materialises a temp project at runtime that depends
# on github.com/andrioid/gastro; the go.mod's `replace` directive
# points at /opt/gastro so gopls can resolve the gastro module from
# on-disk source without network access. Discovered while building
# Steps 1+2 of the live-LSP demo — see tmp/lsp-demo-plan.md under
# 'Runtime project layout'. Without this the shadow generator's
# `gastro.Props()` rewrite fails to type-check and the demo's
# diagnostic + hover quality degrades to near-zero.
COPY --from=build --chown=appuser:appuser /src/go.mod /opt/gastro/go.mod
COPY --from=build --chown=appuser:appuser /src/go.sum /opt/gastro/go.sum
COPY --from=build --chown=appuser:appuser /src/cmd /opt/gastro/cmd
COPY --from=build --chown=appuser:appuser /src/internal /opt/gastro/internal
COPY --from=build --chown=appuser:appuser /src/pkg /opt/gastro/pkg
COPY --from=build --chown=appuser:appuser /src/examples/gastro/go.mod /opt/gastro/examples/gastro/go.mod
COPY --from=build --chown=appuser:appuser /src/examples/gastro/go.sum /opt/gastro/examples/gastro/go.sum

# The example uses gopls's GOMODCACHE for non-gastro deps (chroma,
# goldmark, ...). Without the prepopulated cache, gopls would try a
# `go mod download` on first request — which needs network access
# we don't want at runtime. Bake the build-stage cache into the
# image so analysis is fully offline-capable.
COPY --from=build --chown=appuser:appuser /go/pkg/mod /home/appuser/go/pkg/mod

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
