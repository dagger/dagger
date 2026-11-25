module dagger.io/dagger

go 1.27

retract (
	// retract SDK releases with known issues
	v0.11.7
	// retract engine releases from SDK releases
	[v0.0.0, v0.2.36]
)

require (
	github.com/Khan/genqlient v0.8.1
	github.com/adrg/xdg v0.5.3
	github.com/dagger/otel-go v1.47.1-0.20261124213443-e79f0e10530f
	github.com/dagger/querybuilder v0.0.0-20260402040506-574a5e81cb59
	github.com/mitchellh/go-homedir v1.1.0
	github.com/stretchr/testify v1.12.1
	github.com/vektah/gqlparser/v2 v2.5.36
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
	golang.org/x/sync v0.23.0
)

require (
	github.com/99designs/gqlgen v0.17.94 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/log v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.47.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.48.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
