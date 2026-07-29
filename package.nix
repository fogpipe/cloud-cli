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
  version = "0.91.0"; # bumped by the release pipeline (release-fpcloud.yml)
  baseURL = "https://github.com/fogpipe/cloud-cli/releases/download/v${version}";

  # Per-platform release asset + its hash. The release pipeline rewrites the
  # version above and these hashes on every tag. Until then they are placeholders
  # (lib.fakeHash) so `nix build` fails loudly rather than installing a wrong blob.
  sources = {
    x86_64-linux = {
      asset = "fpcloud-linux-amd64";
      hash = "sha256-6gcnL9uDshYDiDxW7+ipIh/FXLjE6hsO2GuUqdt9Eqk=";
    };
    aarch64-linux = {
      asset = "fpcloud-linux-arm64";
      hash = "sha256-+TYEjHznnqPLf7JTml6hJn5hauvuOp0E6ZxikK1K4yQ=";
    };
    x86_64-darwin = {
      asset = "fpcloud-darwin-amd64";
      hash = "sha256-kTr1Xm2sIoW3BAUB/xyujHzMp6H5cM0Du/vqvCHArLc=";
    };
    aarch64-darwin = {
      asset = "fpcloud-darwin-arm64";
      hash = "sha256-jxiHaU+f6VIVT+DlUyrkb6WjCvJb59t5dStfgmf9iwU=";
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
    license = lib.licenses.unfree; # the fpcloud binary is proprietary; packaging is MIT
    sourceProvenance = [ lib.sourceTypes.binaryNativeCode ];
    mainProgram = "fpcloud";
    platforms = builtins.attrNames sources;
    # maintainers = [ lib.maintainers.<you> ]; # add before a nixpkgs PR
  };
}
