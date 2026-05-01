{lib, ...}: let
  services = ["bank" "exchange" "gateway" "notification" "user"];

  vendorHashes = {
    bank = "sha256-UIufAOnWzZ5vfG/ObL/xlNroZLoLWwvBeNU3fuHS7RY=";
    exchange = "sha256-q3ntU0dZD0wGrnFlCPUyynVoo8b218CiM2Kas5e52ZQ=";
    gateway = "sha256-KijZA42AGZhtafPBhkn8v40pSKjdMdEPgyp04bHr/PI=";
    notification = "sha256-GZ+Iu6fk5SK0RqisO5oa4HLuh4O4ICzHUUojMYbjiec=";
    user = "sha256-xTwsoLN9mCKwUv4iNL4umJMjEko9yOmg0Odnl21emHk=";
  };

  repoSrc = lib.cleanSource ../..;

  mkGoBuild = pkgs: pkgs.buildGoModule.override {go = pkgs.go_1_25;};

  mkBinary = pkgs: name:
    (mkGoBuild pkgs) {
      pname = name;
      version = "0.0.0";
      src = repoSrc;
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

  mkTest = pkgs: name:
    (mkGoBuild pkgs) {
      pname = "${name}-test";
      version = "0.0.0";
      src = repoSrc;
      modRoot = "services/${name}";
      subPackages = ["cmd"];
      vendorHash = vendorHashes.${name};
      env = {
        CGO_ENABLED = "1";
        GOWORK = "off";
      };
      doCheck = true;
      checkPhase = ''
        runHook preCheck
        go test -race -count=1 ./...
        runHook postCheck
      '';
    };

  mkLint = pkgs: name: let
    bin = mkBinary pkgs name;
  in
    pkgs.stdenv.mkDerivation {
      name = "${name}-lint";
      src = repoSrc;
      nativeBuildInputs = [pkgs.go_1_25 pkgs.golangci-lint];
      buildPhase = ''
        runHook preBuild
        export HOME=$TMPDIR
        export GOFLAGS='-mod=vendor'
        export GOPROXY=off
        export GOWORK=off
        export GOCACHE=$TMPDIR/go-cache
        export GOLANGCI_LINT_CACHE=$TMPDIR/lint-cache
        chmod -R u+w services/${name}
        cp -r --no-preserve=mode ${bin.goModules} services/${name}/vendor
        cd services/${name}
        golangci-lint run --timeout=5m ./...
        runHook postBuild
      '';
      installPhase = "touch $out";
    };

  mkImage = n2c: pkgs: name: bin:
    n2c.buildImage {
      name = "ghcr.io/raf-si-2025/banka-3-backend-${name}";
      tag = "latest";
      copyToRoot = [pkgs.cacert];
      config = {
        Entrypoint = ["${bin}/bin/${name}"];
      };
    };

  mkPerSvc = suffix: f:
    lib.listToAttrs (map (name: {
        name =
          if suffix == ""
          then name
          else "${name}-${suffix}";
        value = f name;
      })
      services);
in {
  perSystem = {
    pkgs,
    inputs',
    ...
  }: let
    n2c = inputs'.nix2container.packages.nix2container;

    binaries = mkPerSvc "" (mkBinary pkgs);
    images = mkPerSvc "image" (name: mkImage n2c pkgs name binaries.${name});

    buildChecks = mkPerSvc "build" (name: binaries.${name});
    testChecks = mkPerSvc "test" (mkTest pkgs);
    lintChecks = mkPerSvc "lint" (mkLint pkgs);

    fmtCheck = pkgs.runCommandLocal "fmt-check" {
      nativeBuildInputs = [pkgs.go_1_25];
      src = repoSrc;
    } ''
      cd $src
      bad=$(gofmt -s -l services pkg)
      if [ -n "$bad" ]; then
        echo "Files need gofmt:"
        echo "$bad"
        exit 1
      fi
      touch $out
    '';
  in {
    packages = binaries // images;
    checks = buildChecks // testChecks // lintChecks // {fmt = fmtCheck;};
  };
}
