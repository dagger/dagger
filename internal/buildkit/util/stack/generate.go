package stack

//go:generate protoc -I=. --go_out=. --go_opt=paths=source_relative --go_opt=Mstack.proto=/internal/buildkit/util/stack stack.proto
