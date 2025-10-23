module github.com/dagger/dagger/.dagger

go 1.24.4

require (
	github.com/dagger/dagger v0.0.0-00010101000000-000000000000
	github.com/dagger/dagger/engine/distconsts v0.19.3
)

replace (
	github.com/dagger/dagger => ..
	github.com/dagger/dagger/engine/distconsts => ../engine/distconsts
	github.com/dagger/dagger/sdk/typescript/runtime => ../sdk/typescript/runtime
)

require (
	github.com/99designs/gqlgen v0.17.81
	github.com/BurntSushi/toml v0.3.1
	github.com/Khan/genqlient v0.8.1
	github.com/containerd/platforms v1.0.0-rc.1
	github.com/vektah/gqlparser/v2 v2.5.30
	go.opentelemetry.io/otel v1.38.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.14.2
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.14.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.38.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.38.0
	go.opentelemetry.io/otel/log v0.14.0
	go.opentelemetry.io/otel/sdk v1.38.0
	go.opentelemetry.io/otel/sdk/log v0.14.0
	go.opentelemetry.io/otel/trace v1.38.0
	go.opentelemetry.io/proto/otlp v1.8.0
	golang.org/x/mod v0.29.0
	golang.org/x/sync v0.17.0
	google.golang.org/grpc v1.76.0
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.38.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.38.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0
	go.opentelemetry.io/otel/sdk/metric v1.38.0
	golang.org/x/net v0.46.0 // indirect
	golang.org/x/sys v0.37.0 // indirect
	golang.org/x/text v0.30.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250825161204-c5933d9347a5 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250825161204-c5933d9347a5 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.14.0

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.14.0

replace go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.14.0

replace go.opentelemetry.io/otel/sdk/log => go.opentelemetry.io/otel/sdk/log v0.14.0
