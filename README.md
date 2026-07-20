# fpcloud

The command-line interface for [Fogpipe Cloud](https://cloud.fogpipe.com) — deploy
apps, manage databases, and get scoped `kubectl` access to your projects.

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

fpcloud is a proprietary (unfree) binary, so allow unfree when installing it —
e.g. `nixpkgs.config.allowUnfree = true`, or `--impure` with
`NIXPKGS_ALLOW_UNFREE=1`.

## Quickstart

```sh
fpcloud login                                # browser sign-in
fpcloud org use <org>                        # select your organization
fpcloud project use <project>                # select a project
fpcloud app deploy <app> --image <image>     # deploy or update an app
```

`fpcloud --help` lists everything; `fpcloud <command> --help-llm` prints dense,
machine-readable help for a command and its subtree.

## Licensing

The packaging in this repository — the install script, Nix flake/derivation,
Homebrew formula, and docs — is [MIT](LICENSE). The `fpcloud` binary distributed
via [Releases](https://github.com/fogpipe/cloud-cli/releases) is proprietary,
closed-source software governed separately by Fogpipe.
