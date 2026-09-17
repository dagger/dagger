package terminal

//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. terminal.proto
