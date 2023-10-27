{ lib, stdenv, fetchurl, installShellFiles }:

let
  current = import ./current.nix;
in
stdenv.mkDerivation rec {
  pname = "dagger";
  version = current.version;

  src =
    let
      inherit (stdenv.hostPlatform) system;

      selectSystem = attrs: attrs.${system} or (throw "Unsupported system: ${system}");

      suffix = selectSystem {
        x86_64-linux = "linux_amd64";
        x86_64-darwin = "darwin_amd64";
        aarch64-linux = "linux_arm64";
        aarch64-darwin = "darwin_arm64";
      };
      hash = selectSystem current.hashes;
    in
    fetchurl {
      inherit hash;

      url = "https://github.com/dagger/dagger/releases/download/v${version}/dagger_v${version}_${suffix}.tar.gz";
    };

  # Work around the "unpacker appears to have produced no directories"
  # case that happens when the archive doesn't have a subdirectory.
  sourceRoot = ".";

  installPhase = ''
    runHook preInstall

    mkdir -p $out/bin
    cp dagger $out/bin/

    runHook postInstall
  '';

  nativeBuildInputs = [ installShellFiles ];

  postInstall = lib.optionalString (stdenv.buildPlatform.canExecute stdenv.hostPlatform) ''
    installShellCompletion --cmd dagger \
      --bash <($out/bin/dagger completion bash) \
      --fish <($out/bin/dagger completion fish) \
      --zsh <($out/bin/dagger completion zsh)
  '';
}
