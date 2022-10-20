:::note
The Dagger Go SDK requires [Go 1.15 or later](https://go.dev/doc/install).
:::

Install the Dagger Go SDK using the commands below:

```shell
go get go.dagger.io/dagger@v0.3.0-alpha.1
go mod edit -replace github.com/docker/docker=github.com/docker/docker@v20.10.3-0.20220414164044-61404de7df1a+incompatible
go mod tidy
```
