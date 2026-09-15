package h2c

//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. h2c.proto
