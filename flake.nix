{
  description = "Banka-3-Backend dev shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.flake-utils.url = "github:numtide/flake-utils";

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let pkgs = nixpkgs.legacyPackages.${system};
      in {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go_1_25
            gopls
            gofumpt
            golangci-lint
            buf
            protobuf
            protoc-gen-go
            protoc-gen-go-grpc
            go-migrate
            postgresql
            redis
            sqlc
            go-task
            docker-compose
          ];
          shellHook = ''
            export GOPATH="$PWD/.go"
            export PATH="$GOPATH/bin:$PATH"
          '';
        };
      });
}
