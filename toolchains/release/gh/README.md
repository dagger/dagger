# GitHub CLI

[Daggerverse](https://daggerverse.dev/mod/github.com/sagikazarmark/daggerverse/gh)

## Examples

### Go

```go
dag.Gh().Run("--help")
```

### Shell

Run the following command to see the command line interface:

```shell
dagger call -m "github.com/sagikazarmark/daggerverse/gh@main" --help
```

## To Do

- [ ] Allow getting the binary for other platforms
- [ ] Allow customizing the base image
- [ ] Figure out if a token is always required
- [ ] Add support for GitHub Enterprise
- [ ] Add more examples

## Credits

Inspired by [this module](https://github.com/aweris/daggerverse/tree/main/gh) by [@aweris](https://github.com/aweris).
