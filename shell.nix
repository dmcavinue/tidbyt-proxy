let
  nixpkgs = fetchTarball "https://github.com/NixOS/nixpkgs/tarball/nixos-24.05";
  pkgs = import nixpkgs { config.allowUnfree = true; overlays = []; };
in

let vscode = (pkgs.vscode-with-extensions.override {
  vscodeExtensions = with pkgs.vscode-extensions; [
    golang.Go
  ];
  });
in
pkgs.mkShellNoCC {
  packages = with pkgs; [
    go
  ];

  shellHook =
  ''
    export GOPATH=`pwd`
    export PATH=$GOPATH/bin:$PATH
  '';
}