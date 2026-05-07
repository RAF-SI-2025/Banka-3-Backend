{...}: {
  perSystem = {pkgs, ...}: {
    devShells.default = pkgs.mkShell {
      packages = with pkgs; [
        go_1_25
        gotools
        golangci-lint
        go-task
        protobuf
        protoc-gen-go
        protoc-gen-go-grpc
        postgresql
        grpcurl
      ];
    };
  };
}
