module dagger.io/dagger

go 1.20

replace github.com/dagger/dagger => ../..

// retract engine releases from SDK releases
retract [v0.0.0, v0.2.36]

require (
	github.com/99designs/gqlgen v0.17.31
	github.com/Khan/genqlient v0.6.0
	github.com/adrg/xdg v0.4.0
	github.com/iancoleman/strcase v0.3.0
	github.com/stretchr/testify v1.8.2
	github.com/vektah/gqlparser/v2 v2.5.6
	golang.org/x/exp v0.0.0-20230817173708-d852ddb80c63
	golang.org/x/sync v0.3.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/kr/pretty v0.2.1 // indirect
	github.com/mitchellh/go-homedir v1.1.0
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/sys v0.11.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
