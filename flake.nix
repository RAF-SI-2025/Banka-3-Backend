{
  description = "Banka-3-Backend — Go + gRPC + Postgres + Redis dev shell.";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs = inputs @ {flake-parts, ...}:
    flake-parts.lib.mkFlake {inherit inputs;} {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      perSystem = {pkgs, ...}: {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            # Go toolchain
            go_1_25
            gopls
            gofumpt
            golangci-lint

            # Proto codegen (matches docker/Dockerfile.tools)
            buf
            protobuf
            protoc-gen-go
            protoc-gen-go-grpc

            # Migrations
            go-migrate

            # Local stack clients (psql, redis-cli) + compose runner
            postgresql
            redis
            docker-compose

            # Misc
            gnumake
            jq
            git
            curl
            openssl
          ];

          shellHook = ''
            # Keep `go install`'d binaries (protoc-gen-go etc) out of $HOME
            # and inside the repo so a `git clean -fdx` resets dev state.
            export GOPATH="$PWD/.go"
            export PATH="$GOPATH/bin:$PATH"
            # Workspace mode for IDE/dev convenience. Service builds inside
            # docker still set GOWORK=off (see docker/Dockerfile).
            export GOWORK="$PWD/go.work"
          '';
        };
      };
    };
}
