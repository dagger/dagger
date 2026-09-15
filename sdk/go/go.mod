module dagger.io/dagger

go 1.26.1

retract (
	// retract SDK releases with known issues
	v0.11.7
	// retract engine releases from SDK releases
	[v0.0.0, v0.2.36]
)

require (
	github.com/Khan/genqlient v0.8.1
	github.com/adrg/xdg v0.5.3
	github.com/dagger/querybuilder v0.0.0-20260402040506-574a5e81cb59
	github.com/mitchellh/go-homedir v1.1.0
	github.com/stretchr/testify v1.11.1
	github.com/vektah/gqlparser/v2 v2.5.32
	go.opentelemetry.io/otel v1.41.0
	go.opentelemetry.io/otel/trace v1.41.0
	golang.org/x/sync v0.20.0
)

require (
	github.com/99designs/gqlgen v0.17.89 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.41.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.16.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.16.0
	go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.16.0
	go.opentelemetry.io/otel/sdk/log => go.opentelemetry.io/otel/sdk/log v0.16.0
)
