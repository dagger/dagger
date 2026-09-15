package main

import (
	"strconv"
	"time"
)

type Test struct{}

func (*Test) Fn() string {
	return strconv.Itoa(int(time.Now().UnixNano()))
}
