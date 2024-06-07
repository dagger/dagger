package telemetry

//go:generate protoc -I=./ -I=./opentelemetry-proto --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative servers.proto
