module dagger/release

go 1.26.1

require (
	dagger.io/dagger v1.0.0-beta.17
	github.com/Khan/genqlient v0.8.1
	github.com/dagger/querybuilder v0.0.0-20260402040506-574a5e81cb59
	github.com/go-task/slim-sprig/v3 v3.0.0
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
	golang.org/x/mod v0.37.0
	golang.org/x/sync v0.22.0
	helm.sh/helm/v3 v3.18.4
	sigs.k8s.io/yaml v1.4.0
)

require (
	github.com/99designs/gqlgen v0.17.90 // indirect
	github.com/Masterminds/semver/v3 v3.3.0 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.33 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace (
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.16.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.16.0
	go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.16.0
	go.opentelemetry.io/otel/sdk/log => go.opentelemetry.io/otel/sdk/log v0.16.0
)
