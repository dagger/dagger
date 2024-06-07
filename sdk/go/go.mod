module dagger.io/dagger

go 1.21

// retract engine releases from SDK releases
retract [v0.0.0, v0.2.36]

require (
	github.com/99designs/gqlgen v0.17.44
	github.com/Khan/genqlient v0.7.0
	github.com/adrg/xdg v0.4.0
	github.com/stretchr/testify v1.9.0
	github.com/vektah/gqlparser/v2 v2.5.11
	go.opentelemetry.io/otel v1.26.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.0.0-20240518090000-14441aefdf88
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.2.0-alpha
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.24.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.26.0
	go.opentelemetry.io/otel/log v0.2.0-alpha
	go.opentelemetry.io/otel/sdk v1.26.0
	go.opentelemetry.io/otel/sdk/log v0.2.0-alpha
	go.opentelemetry.io/otel/trace v1.26.0
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa
	golang.org/x/sync v0.7.0
)

require google.golang.org/grpc v1.64.0

require github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect

require (
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-logr/logr v1.4.1 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.20.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sosodev/duration v1.2.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.26.0 // indirect
	go.opentelemetry.io/otel/metric v1.26.0 // indirect
	go.opentelemetry.io/proto/otlp v1.2.0
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
	golang.org/x/text v0.15.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240515191416-fc5f0ca64291 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240515191416-fc5f0ca64291 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
