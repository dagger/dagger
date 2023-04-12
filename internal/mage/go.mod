module github.com/dagger/dagger/internal/mage

go 1.20

require (
	dagger.io/dagger v0.6.0
	github.com/hexops/gotextdiff v1.0.3
	github.com/magefile/mage v1.14.0
	golang.org/x/mod v0.10.0
	golang.org/x/sync v0.1.0
)

require (
	github.com/Khan/genqlient v0.5.0 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/iancoleman/strcase v0.2.0 // indirect
	github.com/stretchr/testify v1.8.2 // indirect
	github.com/vektah/gqlparser/v2 v2.5.1 // indirect
	golang.org/x/sys v0.7.0 // indirect
	golang.org/x/tools v0.8.0 // indirect
)

// needed to resolve "ambiguous import: found package cloud.google.com/go/compute/metadata in multiple modules"
replace cloud.google.com/go => cloud.google.com/go v0.100.2

replace dagger.io/dagger => ../../sdk/go

replace github.com/dagger/dagger => ../../
