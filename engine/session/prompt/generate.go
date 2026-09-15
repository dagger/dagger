package prompt

//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. prompt.proto
