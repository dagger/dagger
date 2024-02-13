module dagger

go 1.21.3

require (
	github.com/99designs/gqlgen v0.17.41
	github.com/Khan/genqlient v0.6.0
	github.com/dagger/dagger v0.9.9
	github.com/moby/buildkit v0.13.0-beta3
	github.com/vektah/gqlparser/v2 v2.5.10
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa
	golang.org/x/sync v0.6.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sosodev/duration v1.1.0 // indirect
)

replace github.com/dagger/dagger => ../
