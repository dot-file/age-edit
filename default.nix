{
  pkgs ? (
    let
      inherit (builtins) fetchTree fromJSON readFile;
      inherit ((fromJSON (readFile ./flake.lock)).nodes) nixpkgs gomod2nix;
    in
    import (fetchTree nixpkgs.locked) {
      overlays = [
        (import "${fetchTree gomod2nix.locked}/overlay.nix")
      ];
    }
  ),
  buildGoApplication ? pkgs.buildGoApplication,
  lib,
}:

let
  # Takes version definition from main.go
  text = builtins.readFile ./main.go;
  sanitizedText = lib.stringAsChars (x: if x == "\t" || x == "\"" then "" else x) text;
  linesList = lib.splitString "\n" sanitizedText;
  versionDefinition = lib.findFirst (x: lib.hasInfix "version" x) "version = 1.0" linesList;
  verDefWordsList = lib.splitString " " versionDefinition;
  version = lib.last verDefWordsList;
in
buildGoApplication {
  inherit version;
  pname = "age-edit";
  pwd = ./.;
  src = ./.;
  modules = ./gomod2nix.toml;
}
