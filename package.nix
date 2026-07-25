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
  version = "0.75.0"; # bumped by the release pipeline (release-fpcloud.yml)
  baseURL = "https://github.com/fogpipe/cloud-cli/releases/download/v${version}";

  # Per-platform release asset + its hash. The release pipeline rewrites the
  # version above and these hashes on every tag. Until then they are placeholders
  # (lib.fakeHash) so `nix build` fails loudly rather than installing a wrong blob.
  sources = {
    x86_64-linux = {
      asset = "fpcloud-linux-amd64";
      hash = "sha256-DTH/IuimQXJtmZWR99CK0CuEZ/qqUUTyyadq7nTKUqk=";
    };
    aarch64-linux = {
      asset = "fpcloud-linux-arm64";
      hash = "sha256-gS83+xyUKAmxSZNzftN/Xhd1HAC3DC19FjzmU2sHYv8=";
    };
    x86_64-darwin = {
      asset = "fpcloud-darwin-amd64";
      hash = "sha256-oQg7UFOOqUe75+4HUtjuCpQihrJTn6Qi+VLumYJ2TsQ=";
    };
    aarch64-darwin = {
      asset = "fpcloud-darwin-arm64";
      hash = "sha256-CH4vqmPszZFUBuLPSwCh/YrA1WC9gd2qb5mUctfnReE=";
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
