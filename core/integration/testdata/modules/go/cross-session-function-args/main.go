package main

import (
	"strconv"
	"time"
)

type Test struct {
	ArbitraryString string
}

func New(
	// +optional
	parentArg string,
) Test {
	return Test{ArbitraryString: parentArg}
}

func (*Test) Fn(
	// +optional
	i int,
	// +optional
	s string,
) string {
	return strconv.Itoa(int(time.Now().UnixNano()))
}
