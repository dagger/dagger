# Attestations

BuildKit supports creating and attaching attestations to build artifacts.
Generated attestations use the [in-toto attestation format](https://github.com/in-toto/attestation).

The currently supported attestation types are:

- [SBOMs](./sbom.md)
- [SLSA Provenance](./slsa-provenance.md)

Upon generation, attestations are attached differently to the export result:

- For the `image`, `oci` and `docker` exporters, attestations are exported
  using the attached [attestation storage](./attestation-storage.md).
- For the `local` and `tar` exporters, attestations are written to separate
  files within the output directory.
