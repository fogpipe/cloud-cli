# fpcloud — the Fogpipe Cloud CLI, distributed as a prebuilt binary.
#
# This single derivation is used two ways:
#   * flake.nix in this repo (so anyone can `nix run`/add it to their inputs), and
#   * a future nixpkgs submission (copy this to pkgs/by-name/fp/fpcloud/package.nix).
#
# Fetches the released artifact per platform rather than building from source
# (sourceProvenance = binaryNativeCode), even though the source is right here and
# Apache-2.0. buildGoModule cannot produce a working CLI: Google rejects the
# token exchange without the client secret even under PKCE, the secret is baked
# via ldflags at release time and must not live in a repo, so a from-source build
# would install fine and fail at `fpcloud login`.
{
  lib,
  stdenvNoCC,
  fetchurl,
}:
let
  version = "0.117.0"; # bumped by the release pipeline (.github/workflows/release.yml)
  baseURL = "https://github.com/fogpipe/cloud-cli/releases/download/v${version}";

  # Per-platform release asset + its hash. The release pipeline rewrites the
  # version above and these hashes on every tag. Until then they are placeholders
  # (lib.fakeHash) so `nix build` fails loudly rather than installing a wrong blob.
  sources = {
    x86_64-linux = {
      asset = "fpcloud-linux-amd64";
      hash = "sha256-p2FcXEI29cAdfZsEEKDsHzwUrza/BjMdVyuh5+QZbwI=";
    };
    aarch64-linux = {
      asset = "fpcloud-linux-arm64";
      hash = "sha256-KvraDvLQUxTrC+zfGsTmwWxEEJHGlv56L/mQSPzxLS8=";
    };
    x86_64-darwin = {
      asset = "fpcloud-darwin-amd64";
      hash = "sha256-xwas30o/kbkh8+lIjw8gn110SA2DOslzGlarQdLOWOk=";
    };
    aarch64-darwin = {
      asset = "fpcloud-darwin-arm64";
      hash = "sha256-I7ebsABPLieETQ2k/MsR6K6R9LQuWmfsUXsuIK9JOIM=";
    };
  };

  system = stdenvNoCC.hostPlatform.system;
  source =
    sources.${system} or (throw "fpcloud: unsupported system ${system}");
in
stdenvNoCC.mkDerivation {
  pname = "fpcloud";
  inherit version;

  src = fetchurl {
    url = "${baseURL}/${source.asset}";
    inherit (source) hash;
  };

  # A single prebuilt binary — nothing to unpack; it IS the download.
  dontUnpack = true;

  # Pure-Go static binary: no interpreter/rpath to patch on Linux, so plain install.
  installPhase = ''
    runHook preInstall
    install -Dm755 "$src" "$out/bin/fpcloud"
    runHook postInstall
  '';

  meta = {
    description = "Fogpipe Cloud CLI — deploy apps, manage databases, domains, and object storage";
    homepage = "https://github.com/fogpipe/cloud-cli";
    license = lib.licenses.asl20;
    sourceProvenance = [ lib.sourceTypes.binaryNativeCode ];
    mainProgram = "fpcloud";
    platforms = builtins.attrNames sources;
    # maintainers = [ lib.maintainers.<you> ]; # add before a nixpkgs PR
  };
}
