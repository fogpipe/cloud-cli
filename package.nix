# fpcloud — the Fogpipe Cloud CLI, distributed as a prebuilt proprietary binary.
#
# This single derivation is used two ways:
#   * flake.nix in this repo (so anyone can `nix run`/add it to their inputs), and
#   * a future nixpkgs submission (copy this to pkgs/by-name/fp/fpcloud/package.nix).
#
# The binary is closed-source, so this fetches the released artifact per platform
# rather than building from source (meta.license = unfree, sourceProvenance =
# binaryNativeCode). The packaging around it is MIT (see LICENSE).
{
  lib,
  stdenvNoCC,
  fetchurl,
}:
let
  version = "0.64.0"; # bumped by the release pipeline (release-fpcloud.yml)
  baseURL = "https://github.com/fogpipe/cloud-cli/releases/download/v${version}";

  # Per-platform release asset + its hash. The release pipeline rewrites the
  # version above and these hashes on every tag. Until then they are placeholders
  # (lib.fakeHash) so `nix build` fails loudly rather than installing a wrong blob.
  sources = {
    x86_64-linux = {
      asset = "fpcloud-linux-amd64";
      hash = "sha256-8/x0DJFar9H+JW16QjFCzU1OMzsDIAr3B9nf5xqUjRs=";
    };
    aarch64-linux = {
      asset = "fpcloud-linux-arm64";
      hash = "sha256-1d9NbsIa6mMdoTg6e0oa9n7Tu5+YxBfXyORpKCLW5+U=";
    };
    x86_64-darwin = {
      asset = "fpcloud-darwin-amd64";
      hash = "sha256-5/vNVrQNloA91ECsgUI7JpGnSICjqZcCOcY2f4i4V2k=";
    };
    aarch64-darwin = {
      asset = "fpcloud-darwin-arm64";
      hash = "sha256-bNv8IV1rTjDXv3fhiIWd29ThxANqjKkC3UZvTQLe+h4=";
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
    description = "Fogpipe Cloud CLI — deploy apps, manage databases, and get scoped kubectl access";
    homepage = "https://github.com/fogpipe/cloud-cli";
    license = lib.licenses.unfree; # the fpcloud binary is proprietary; packaging is MIT
    sourceProvenance = [ lib.sourceTypes.binaryNativeCode ];
    mainProgram = "fpcloud";
    platforms = builtins.attrNames sources;
    # maintainers = [ lib.maintainers.<you> ]; # add before a nixpkgs PR
  };
}
