module github.com/dagger/dagger/.dagger

go 1.25.8

replace (
	github.com/dagger/dagger => ..
	github.com/dagger/dagger/engine/distconsts => ../engine/distconsts
	github.com/dagger/dagger/sdk/typescript/runtime => ../sdk/typescript/runtime
)

require (
	github.com/dagger/dagger v0.0.0-00010101000000-000000000000
	github.com/dagger/otel-go v1.41.1-0.20260303185236-072f65948887
	go.opentelemetry.io/otel/sdk v1.41.0
)

require (
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/jedevc/diffparser v0.0.0-20251006145221-cebbf07eb779 // indirect
	github.com/lucasb-eyer/go-colorful v1.3.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.17.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.17.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.41.0 // indirect
	go.opentelemetry.io/otel/log v0.17.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.17.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.41.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

require (
	dagger.io/dagger v0.20.3
	github.com/99designs/gqlgen v0.17.81 // indirect
	github.com/Khan/genqlient v0.8.1
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/sosodev/duration v1.3.1 // indirect
	github.com/vektah/gqlparser/v2 v2.5.30
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.41.0
	go.opentelemetry.io/otel/metric v1.41.0 // indirect
	go.opentelemetry.io/otel/trace v1.41.0
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
)

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.16.0

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.16.0

replace go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.16.0

replace go.opentelemetry.io/otel/sdk/log => go.opentelemetry.io/otel/sdk/log v0.16.0
