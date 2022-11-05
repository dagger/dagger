{ pkgs ? import <nixpkgs> { } }:
pkgs.mkShell {
  # nativeBuildInputs is usually what you want -- tools you need to run
  nativeBuildInputs = with pkgs; [
    protobuf
    protoc-gen-go
    protoc-gen-go-grpc
  ];
}
