{lib, ...}: let
  services = ["bank"];

  vendorHashes = {
    bank = "sha256-UIufAOnWzZ5vfG/ObL/xlNroZLoLWwvBeNU3fuHS7RY=";
  };

  mkBinary = pkgs: name:
    pkgs.buildGo125Module {
      pname = name;
      version = "0.0.0";
      src = lib.cleanSource ../.;
      modRoot = "services/${name}";
      subPackages = ["cmd"];
      vendorHash = vendorHashes.${name};
      env = {
        CGO_ENABLED = "0";
        GOWORK = "off";
      };
      ldflags = ["-s" "-w"];
      postInstall = ''
        mv $out/bin/cmd $out/bin/${name}
      '';
    };

  mkImage = pkgs: name: bin:
    pkgs.dockerTools.buildLayeredImage {
      name = "ghcr.io/raf-si-2025/banka-3-backend-${name}";
      tag = "latest";
      contents = [pkgs.cacert];
      config.Cmd = ["${bin}/bin/${name}"];
    };
in {
  perSystem = {pkgs, ...}: let
    binaries = lib.genAttrs services (mkBinary pkgs);
    images =
      lib.mapAttrs'
      (name: bin: lib.nameValuePair "${name}-image" (mkImage pkgs name bin))
      binaries;
    buildChecks =
      lib.mapAttrs'
      (name: bin: lib.nameValuePair "${name}-build" bin)
      binaries;
  in {
    packages = binaries // images;
    checks = buildChecks;
  };
}
