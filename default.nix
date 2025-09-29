{ buildGoModule
, fetchFromGitHub
, lib
}:

let
  # Finds version definition in main.go
  text = builtins.readFile ./main.go;
  sanitizedText = lib.stringAsChars (x: if x == "\t" || x == "\"" then "" else x) text;
  linesList = lib.splitString "\n" sanitizedText;
  versionDefinition = lib.findFirst (x: lib.hasInfix "version" x) "version = 1.0" linesList;
  verDefWordsList = lib.splitString " " versionDefinition;
  version = lib.last verDefWordsList;
in
buildGoModule {
  pname = "age-edit";
  inherit version;

  src = ./.;

  vendorHash = "sha256-KSs+zYcF5xIJh0oD04OQ977EmRIz4EiYvrw+TUnW/sw=";
}
