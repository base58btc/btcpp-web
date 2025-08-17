{
  description = "bitcoin++ website";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  inputs.flake-utils.url = "github:numtide/flake-utils";

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
	    poppler-utils
            bashInteractive
            jq
            go
            tailwindcss
            air 
            ffmpeg
	    git
          ];
          # Automatically run ??? when entering the shell.
          #shellHook = "???";
        };
      });
}
