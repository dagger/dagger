package auth

//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. auth.proto
