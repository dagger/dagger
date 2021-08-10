package rg

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/azure/resourcegroup"
	"alpha.dagger.io/random"
)

suffix: random.#String & {
	seed: "azrg"
}

rg: resourcegroup.#ResourceGroup & {
	config:     azure.#Config
	rgName:     "rg-test-\(suffix.out)"
	rgLocation: "eastus2"
}
