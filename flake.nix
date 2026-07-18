{
  description = "fpcloud — the Fogpipe Cloud CLI (prebuilt binary)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      # The systems fpcloud ships binaries for.
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f system);
      # allowUnfree, because the fpcloud binary is proprietary.
      pkgsFor = system: import nixpkgs {
        inherit system;
        config.allowUnfree = true;
      };
    in
    {
      packages = forAllSystems (
        system:
        let
          fpcloud = (pkgsFor system).callPackage ./package.nix { };
        in
        {
          inherit fpcloud;
          default = fpcloud;
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.fpcloud}/bin/fpcloud";
        };
      });
    };
}
