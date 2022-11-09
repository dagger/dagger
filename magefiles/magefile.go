//go:build mage
// +build mage

package main

import (
	//mage:import
	_ "github.com/dagger/dagger/magefiles/targets"

	//mage:import sdk
	_ "github.com/dagger/dagger/magefiles/sdk"
)
