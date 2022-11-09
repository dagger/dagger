# stable `go.mod` for mage bootstrapping

This directory contains a `go.mod` and `go.sum` which determine the SDK version
to use for bootstrapping.

These files are stored in their own directory because we only sometimes want to
actually use them. The `mage devDagger` and `mage stable` targets both symlink
them under `magefiles/` just-in-time and remove them after building.

To run Go commands against these files as if they were in the regular
`magefiles/` source tree, use the `go` script:

```sh
./magefiles/mod/go get dagger.io/dagger@main
./magefiles/mod/go mod tidy
```
