package sshforward

//go:generate:container dag://go-base
//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. ssh.proto
