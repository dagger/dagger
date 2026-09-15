package store

//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. basic.proto
