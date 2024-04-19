# Create BuildKit certificates

This [bake definition](docker-bake.hcl) can be used for creating certificates:

```bash
SAN="127.0.0.1" docker buildx bake https://github.com/moby/buildkit.git#master:examples/create-certs
```
