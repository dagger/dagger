---
slug: /1224/self-signed-certificates/
---

# Running Dagger with self-signed certificates

The connection to a container registry or to a remote docker daemon might require the need to add self-signed CA: `x509: certificate signed by unknown authority`.

These operations are being run inside the buildkitd context and require you to mount your certificates inside your buildkit instance.

## Running a custom buildkit in Docker

To run a customized Buildkit version with Docker, this can be done using the below command. You can add as many certificate as you need:

```shell
docker run --net=host -d --restart always -v $PWD/my-cert.pem:/etc/ssl/certs/my-cert.pem --name dagger-buildkitd --privileged moby/buildkit:latest
```

To connect your Dagger client to this custom instance, [please follow these steps](1223-custom-buildkit.md)
