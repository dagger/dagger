module dagger.io/dagger

go 1.23.0

toolchain go1.23.6

retract (
	// retract SDK releases with known issues
	v0.11.7
	// retract engine releases from SDK releases
	[v0.0.0, v0.2.36]
)

require (
	github.com/99designs/gqlgen v0.17.78
	github.com/Khan/genqlient v0.8.1
	github.com/adrg/xdg v0.5.3
	github.com/mitchellh/go-homedir v1.1.0
	github.com/stretchr/testify v1.11.1
	github.com/vektah/gqlparser/v2 v2.5.30
	go.opentelemetry.io/otel v1.37.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.12.2
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.12.2
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.32.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.32.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.32.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.32.0
	go.opentelemetry.io/otel/log v0.12.2
	go.opentelemetry.io/otel/metric v1.37.0
	go.opentelemetry.io/otel/sdk v1.37.0
	go.opentelemetry.io/otel/sdk/log v0.12.2
	go.opentelemetry.io/otel/sdk/metric v1.37.0
	go.opentelemetry.io/otel/trace v1.37.0
	go.opentelemetry.io/proto/otlp v1.6.0
	golang.org/x/sync v0.16.0
	google.golang.org/grpc v1.75.0
)

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.26.3 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.32.0 // indirect
	golang.org/x/net v0.42.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
	golang.org/x/text v0.27.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250707201910-8d1bb00bc6a7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250707201910-8d1bb00bc6a7 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.12.2
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.12.2
	go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.12.2
	go.opentelemetry.io/otel/sdk/log => go.opentelemetry.io/otel/sdk/log v0.12.2
)
