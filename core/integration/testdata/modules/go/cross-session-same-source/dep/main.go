package main

import (
	"strconv"
	"time"
)

type Dep struct{}

func (*Dep) Fn(rand string) string {
	return strconv.Itoa(int(time.Now().UnixNano()))
}
