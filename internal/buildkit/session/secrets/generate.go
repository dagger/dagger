package secrets

//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. secrets.proto
