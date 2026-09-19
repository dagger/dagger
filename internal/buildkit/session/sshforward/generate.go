package sshforward

//go:generate:include *.proto
//go:generate protoc --gogoslick_out=plugins=grpc:. ssh.proto
