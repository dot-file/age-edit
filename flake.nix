{
  description = "Editor wrapper for files encrypted with age.";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (system:
    let
      pkgs = import nixpkgs { inherit system; };
    in
    {
      packages = {
        age-edit = pkgs.callPackage ./. { };
        default = self.packages.${system}.age-edit;
      };
    })
    // {
      overlays.default = final: prev: {
        inherit (self.packages.${final.system}) age-edit;
      };
    };
}
