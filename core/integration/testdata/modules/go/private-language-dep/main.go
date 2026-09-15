package main

import (
	"github.com/dagger/dagger-test-modules/privatedeps/pkg/cooldep"
)

type Foo struct{}

func (m *Foo) HowCoolIsDagger() string {
	return cooldep.HowCoolIsThat
}
