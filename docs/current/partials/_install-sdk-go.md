:::note
The Dagger Go SDK requires [Go 1.15 or later](https://go.dev/doc/install).
:::

From your existing Go module, install the Dagger Go SDK using the commands below:

```shell
go get dagger.io/dagger@latest
go mod edit -replace github.com/docker/docker=github.com/docker/docker@v20.10.3-0.20220414164044-61404de7df1a+incompatible
```

:::note
The `replace` statement is currently needed due to one of Dagger's dependencies using a `replace` statement in its Go module. Learn more in this [GitHub tracking issue](https://github.com/dagger/dagger/issues/3391).
:::

:::note
Use of `go get -u` may currently result in a transitive dependency being upgraded to an incompatible version and errors like `filesync.go:112:20: cannot use dir.Map (variable of type func(string, *types.Stat) bool) as type fsutil.MapFunc in struct literal`.

At this time, we recommend avoiding `-u` unless necessary to upgrade other unrelated dependencies. If you need to use it, you can patch the problem afterwards by running `go get github.com/tonistiigi/fsutil@v0.0.0-20220115021204-b19f7f9cb274`.
:::

After importing `dagger.io/dagger` in your Go module code, run the following command to update `go.sum`:

```shell
go mod tidy
```
