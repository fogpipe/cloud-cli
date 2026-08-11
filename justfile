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
