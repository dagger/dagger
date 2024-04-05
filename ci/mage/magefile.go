//go:build mage
// +build mage

package main

import (
	//mage:import
	_ "github.com/dagger/dagger/ci/mage"

	//mage:import sdk
	_ "github.com/dagger/dagger/ci/mage/sdk"
)
