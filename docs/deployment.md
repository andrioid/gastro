# Deployment

Gastro applications compile to a single binary with embedded templates and static assets. Deploy by copying one file.

## Building for Production

Generate the Go code and cross-compile for your target platform:

```bash
# Generate Go code from .gastro files
gastro generate

# Cross-compile for Linux
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/myapp .

# Deploy the single binary
scp dist/myapp server:/opt/myapp
```

Or use the shorthand:

```bash
# Or use gastro build for generate + compile
gastro build
./app
```

The resulting binary contains everything: your Go handlers, compiled templates, and static assets from `static/`. No runtime dependencies.

## Docker

A multi-stage Dockerfile keeps the image small. The build stage compiles everything, and the runtime stage contains only the binary:

```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src

# Install the gastro CLI
COPY . /gastro-src
RUN cd /gastro-src && go build -o /usr/local/bin/gastro ./cmd/gastro/

# Copy project files
COPY examples/gastro/ .

# Generate and build
RUN gastro generate
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app .

FROM alpine:3
RUN adduser -D -u 1000 appuser
USER appuser
COPY --from=build /app /app
EXPOSE 4242
CMD ["/app"]
```

The runtime image uses Alpine Linux with a non-root user for security. The final image is typically under 20MB.

## Environment Variables

The only configuration is the `PORT` environment variable:

```bash
# Set the port via environment variable
PORT=8080 ./myapp

# In Docker
docker run -p 8080:8080 -e PORT=8080 myapp
```

If `PORT` is not set, the server defaults to port 4242.

## Platform Guides

The Docker image works with any container platform:

- **Fly.io** — `fly launch` auto-detects the Dockerfile
- **Railway** — connect your repo, Railway builds from the Dockerfile
- **Google Cloud Run** — `gcloud run deploy --source .`
- **AWS ECS / Fargate** — build the image and push to ECR
- **Any VPS** — copy the binary directly with `scp`

Since Gastro builds a static binary with no runtime dependencies, you can also deploy without Docker by copying the binary to any Linux server.
