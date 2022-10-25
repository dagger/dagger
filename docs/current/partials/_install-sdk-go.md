:::note
The Dagger Go SDK requires [Go 1.15 or later](https://go.dev/doc/install).
:::

From an existing Go module, you can install the Dagger Go SDK using the commands below:

```shell
go get dagger.io/dagger@latest
go mod edit -replace github.com/docker/docker=github.com/docker/docker@v20.10.3-0.20220414164044-61404de7df1a+incompatible
```

:::note
The replace statement is currently needed due to one of Dagger's dependencies using a replace statement in their Go module. There is an issue tracking fixes to this [here](https://github.com/dagger/dagger/issues/3391).
:::

Once you've added code to your module that imports `dagger.io/dagger` then you will need to run the following to update `go.sum`:

```shell
go mod tidy
```
