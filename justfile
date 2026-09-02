default:
    @just --list

# Build the CLI into bin/ with the version stamped from the git description
build:
    go build -ldflags "-X github.com/fogpipe/cloud-cli/pkg/cli.version=$(git describe --tags --always --dirty)" -o bin/fpcloud ./cmd/fpcloud

test:
    go test ./...

lint:
    go vet ./...
    gofmt -l .

# Regenerate the third-party attribution the binary embeds (CI fails on a stale file)
licenses:
    deno run --allow-read --allow-write --allow-run --allow-env scripts/third-party-licenses.ts

# Fail if the embedded attribution no longer matches what the binary links
licenses-check:
    deno run --allow-read --allow-run --allow-env scripts/third-party-licenses.ts --check
