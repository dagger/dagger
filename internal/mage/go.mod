module github.com/dagger/dagger/internal/mage

go 1.21

require (
	dagger.io/dagger v0.10.2
	github.com/dagger/dagger v0.10.2
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/hexops/gotextdiff v1.0.3
	github.com/magefile/mage v1.15.0
	github.com/moby/buildkit v0.13.0-beta3
	github.com/opencontainers/image-spec v1.1.0-rc5
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa
	golang.org/x/mod v0.14.0
	golang.org/x/sync v0.6.0
)

require (
	github.com/99designs/gqlgen v0.17.41 // indirect
	github.com/Khan/genqlient v0.6.0 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sosodev/duration v1.1.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.10 // indirect
	golang.org/x/sys v0.17.0 // indirect
)

replace github.com/dagger/dagger => ../../
