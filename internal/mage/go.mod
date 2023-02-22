module github.com/dagger/dagger/internal/mage

go 1.20

require (
	dagger.io/dagger v0.4.4
	github.com/dagger/dagger v0.0.0-00010101000000-000000000000
	github.com/hexops/gotextdiff v1.0.3
	github.com/magefile/mage v1.14.0
	golang.org/x/mod v0.7.0
	golang.org/x/sync v0.1.0
)

require (
	github.com/Khan/genqlient v0.5.0 // indirect
	github.com/adrg/xdg v0.4.0 // indirect
	github.com/iancoleman/strcase v0.2.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.1 // indirect
	golang.org/x/sys v0.5.0 // indirect
)

// needed to resolve "ambiguous import: found package cloud.google.com/go/compute/metadata in multiple modules"
replace cloud.google.com/go => cloud.google.com/go v0.100.2

replace github.com/dagger/dagger => ../../
