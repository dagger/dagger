module github.com/dagger/dagger/magefiles

go 1.19

require (
	dagger.io/dagger v0.4.1-0.20221109161242-ed1561de4e87
	github.com/hexops/gotextdiff v1.0.3
	github.com/magefile/mage v1.14.0
	golang.org/x/mod v0.6.0-dev.0.20220106191415-9b9b3d81d5e3
)

require (
	github.com/Khan/genqlient v0.5.0 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/iancoleman/strcase v0.2.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/vektah/gqlparser/v2 v2.5.1 // indirect
	golang.org/x/sync v0.0.0-20220722155255-886fb9371eb4 // indirect
	golang.org/x/sys v0.0.0-20220811171246-fbc7d0a398ab // indirect
)

replace github.com/docker/docker => github.com/docker/docker v20.10.3-0.20220414164044-61404de7df1a+incompatible
