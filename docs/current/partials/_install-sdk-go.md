:::note
The Dagger Go SDK requires [Go 1.15 or later](https://go.dev/doc/install).
:::

From your existing Go module, install the Dagger Go SDK using the commands below:

```shell
go get dagger.io/dagger@latest
```

After importing `dagger.io/dagger` in your Go module code, run the following command to update `go.sum`:

```shell
go mod tidy
```
