package types

//go:generate:include *.proto
//go:generate protoc -I=. -I=../../../ --gogoslick_out=. stat.proto
