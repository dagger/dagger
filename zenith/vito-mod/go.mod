module vito-mod

go 1.21.0

replace dagger.io/dagger => github.com/shykes/dagger/sdk/go v0.0.0-20230912080048-61eaca787720

require (
	dagger.io/dagger v0.0.0-00010101000000-000000000000
	github.com/Khan/genqlient v0.6.0
)

require (
	github.com/99designs/gqlgen v0.17.31 // indirect
	github.com/vektah/gqlparser/v2 v2.5.6 // indirect
	golang.org/x/sync v0.3.0 // indirect
)
