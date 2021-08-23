package resourcegroup

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/azure/resourcegroup"
	"alpha.dagger.io/random"
)

TestSuffix: random.#String & {
	seed: "azrg"
}

TestRG: resourcegroup.#ResourceGroup & {
	config:     azure.#Config
	rgName:     "rg-test-\(TestSuffix.out)"
	rgLocation: "eastus2"
}
