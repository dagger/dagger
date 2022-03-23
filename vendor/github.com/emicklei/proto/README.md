# proto

[![Build Status](https://travis-ci.org/emicklei/proto.png)](https://travis-ci.org/emicklei/proto)
[![Go Report Card](https://goreportcard.com/badge/github.com/emicklei/proto)](https://goreportcard.com/report/github.com/emicklei/proto)
[![GoDoc](https://godoc.org/github.com/emicklei/proto?status.svg)](https://godoc.org/github.com/emicklei/proto)

Package in Go for parsing Google Protocol Buffers [.proto files version 2 + 3] (https://developers.google.com/protocol-buffers/docs/reference/proto3-spec)

### install

    go get -u -v github.com/emicklei/proto

### usage

	package main

	import (
		"fmt"
		"os"

		"github.com/emicklei/proto"
	)

	func main() {
		reader, _ := os.Open("test.proto")
		defer reader.Close()

		parser := proto.NewParser(reader)
		definition, _ := parser.Parse()

		proto.Walk(definition,
			proto.WithService(handleService),
			proto.WithMessage(handleMessage))
	}

	func handleService(s *proto.Service) {
		fmt.Println(s.Name)
	}

	func handleMessage(m *proto.Message) {
		fmt.Println(m.Name)
	}

### validation

Current parser implementation is not completely validating `.proto` definitions.
In many but not all cases, the parser will report syntax errors when reading unexpected charaters or tokens.
Use some linting tools (e.g. https://github.com/uber/prototool) or `protoc` for full validation.

### contributions

See [proto-contrib](https://github.com/emicklei/proto-contrib) for other contributions on top of this package such as protofmt, proto2xsd and proto2gql.
[protobuf2map](https://github.com/emicklei/protobuf2map) is a small package for inspecting serialized protobuf messages using its `.proto` definition.

© 2017, [ernestmicklei.com](http://ernestmicklei.com).  MIT License. Contributions welcome.