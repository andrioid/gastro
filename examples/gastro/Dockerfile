FROM golang:1.26-alpine AS build
WORKDIR /src

# Copy the full gastro source (needed for the CLI and replace directive)
COPY go.mod go.sum* ./
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
# Markdown source referenced by {{ markdown "../../../../docs/*.md" }}
# directives in examples/gastro/pages/docs/*.gastro.
COPY docs/ docs/

# Build the gastro CLI
RUN go build -o /usr/local/bin/gastro ./cmd/gastro/

# Copy the website example into a path where the replace directive
# (replace github.com/andrioid/gastro => ../..) resolves to /src
COPY examples/gastro/ /src/examples/gastro/
WORKDIR /src/examples/gastro

# Generate Go code from .gastro files and build the binary
RUN gastro generate
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /gastro-website .

FROM alpine:3
RUN adduser -D -u 1000 appuser
USER appuser
COPY --from=build /gastro-website /gastro-website
EXPOSE 4242
CMD ["/gastro-website"]
