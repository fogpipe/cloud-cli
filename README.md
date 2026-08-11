# fpcloud

The command-line interface for [Fogpipe Cloud](https://cloud.fogpipe.com) — deploy
apps, manage databases, attach domains, and serve object storage.

## Install

### Shell (macOS / Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/fogpipe/cloud-cli/main/install.sh | sh
```

Pin a version or install location with `FPCLOUD_VERSION` / `FPCLOUD_INSTALL_DIR`.

### Homebrew

```sh
brew tap fogpipe/cloud-cli https://github.com/fogpipe/cloud-cli
brew install fogpipe/cloud-cli/fpcloud
```

### Nix

Run it directly:

```sh
nix run github:fogpipe/cloud-cli
```

Or add it to your flake:

```nix
{
  inputs.fpcloud.url = "github:fogpipe/cloud-cli";
  # then, in your outputs, for a given system:
  #   fpcloud.packages.${system}.default
}
```

## Update

Update through the channel you installed from:

| Installed with | Update with |
| --- | --- |
| the shell script | `fpcloud upgrade` |
| Homebrew | `brew upgrade fpcloud` |
| a Nix flake input | `nix flake update fpcloud`, then re-enter the dev shell |
| `nix profile install` | `nix profile upgrade fpcloud` |

`fpcloud upgrade` replaces the binary in place with the version your control
plane advertises, so the CLI tracks the API you talk to. It only works for the
shell install: a Nix-installed fpcloud lives in the read-only store and can't
replace itself, so `fpcloud upgrade` prints the commands above instead.

## Quickstart

```sh
fpcloud login                                # browser sign-in
fpcloud org use <org>                        # select your organization
fpcloud project use <project>                # select a project
fpcloud app deploy <app> --image <image>     # deploy or update an app
```

`fpcloud --help` lists everything; `fpcloud <command> --help-llm` prints dense,
machine-readable help for a command and its subtree.

## Source

This repository is the CLI. `cmd/fpcloud` is a thin main over `pkg/cli`, which
holds the command tree, and `pkg/client` is the Go SDK for the Fogpipe REST API
— usable on its own:

```go
import "github.com/fogpipe/cloud-cli/pkg/client"
```

```sh
go build ./...
go test ./...
```

A binary you build yourself is the same one we release — nothing is injected at
release time. Google requires a client secret in the OAuth token exchange even
under PKCE, and a native app cannot keep one, so the platform holds it and
brokers the exchange; `fpcloud login` works from a plain `go build`.

The API this speaks to is published: `openapi.yaml`, `/docs` and `/llms.txt` on
your platform host.

Read rather than contributed to: the source is here so you can audit what holds
your identity, mints your tokens and tunnels to your database. There is no
contribution process, and issues here are not a support channel.

## Licensing

[Apache-2.0](LICENSE), the licence every public Fogpipe repository carries.

Releases up to and including v0.117.0 were published under MIT, and that grant
stands for those versions.
