package resourcegroup

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/random"
)

TestConfig: azConfig: azure.#Config & {
	region: "eastus2"
}

TestResourceGroupConfig: #StorageAccount & {
	suffix: random.#String & {
		seed: "azst"
	}
	config:          TestConfig.azConfig
	rgName:          "rg-test-\(suffix.out)"
	accountLocation: "eastus2"
}
