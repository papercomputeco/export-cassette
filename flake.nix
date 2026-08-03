{
  description = "Export cassette - Development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    dagger.url = "github:dagger/nix";
    dagger.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { self, nixpkgs, flake-utils, dagger }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = [
            # Go toolchain
            pkgs.go_1_26
            pkgs.gotools
            pkgs.sqlc

            # Build tools
            pkgs.gnumake
            dagger.packages.${system}.dagger
          ];

          shellHook = ''
            echo "Export cassette development environment"
            echo ""
            echo "Go version: $(go version)"
            echo ""
            echo "Available make targets:"
            make help
          '';
        };
      }
    );
}
