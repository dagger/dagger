package resourcegroup

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/random"
)

TestConfig: azConfig: azure.#Config & {
	region: "eastus2"
}

TestResourceGroupConfig: #ResourceGroup & {
	suffix: random.#String & {
		seed: "azrg"
	}
	config:     TestConfig.azConfig
	rgName:     "rg-test-\(suffix.out)"
	rgLocation: "eastus2"
}
