module github.com/dagger/dagger/internal/mage

go 1.20

require (
	dagger.io/dagger v0.4.4
	github.com/docker/docker v23.0.0-rc.1+incompatible
	github.com/hexops/gotextdiff v1.0.3
	github.com/magefile/mage v1.14.0
	golang.org/x/mod v0.7.0
	golang.org/x/sync v0.1.0
)

require (
	github.com/Khan/genqlient v0.5.0 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/iancoleman/strcase v0.2.0 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.1 // indirect
	golang.org/x/sys v0.4.0 // indirect
)

replace github.com/docker/docker => github.com/docker/docker v20.10.3-0.20220414164044-61404de7df1a+incompatible
