# fpcloud — the Fogpipe Cloud CLI, built from the source in this repository.
#
# This single derivation is used two ways:
#   * flake.nix in this repo (so anyone can `nix run`/add it to their inputs), and
#   * a nixpkgs submission (copy this to pkgs/by-name/fp/fpcloud/package.nix and
#     swap `src = ./.` for a fetchFromGitHub pinned to the release tag).
#
# Built from source, not repackaged from a release asset: nixpkgs asks for that
# wherever the source allows it, and here it does — the CLI holds no Google
# client secret, because the token exchange is brokered by the platform
# (pkg/cli/login.go). A binary drop would also have made this unfree-adjacent for
# no reason; the source is Apache-2.0.
{
  lib,
  buildGoModule,
}:
buildGoModule (finalAttrs: {
  pname = "fpcloud";
  version = "0.151.0"; # bumped by the release pipeline (.github/workflows/release.yml)

  src = ./.;

  vendorHash = "sha256-txsSh5vq1/5bXC55My+vYTr7nGnRE6d18tY3UUvyfnM=";

  subPackages = [ "cmd/fpcloud" ];

  # Same stamp the release workflow applies, so `fpcloud version` agrees with the
  # package version instead of reporting "dev".
  ldflags = [
    "-s"
    "-w"
    "-X github.com/fogpipe/cloud-cli/pkg/cli.version=${finalAttrs.version}"
  ];

  meta = {
    description = "Fogpipe Cloud CLI — deploy apps, manage databases, domains, and object storage";
    homepage = "https://github.com/fogpipe/cloud-cli";
    license = lib.licenses.asl20;
    mainProgram = "fpcloud";
    platforms = lib.platforms.unix;
  };
})
