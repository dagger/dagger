package storage

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/azure/resourcegroup"
	"alpha.dagger.io/azure/storage"
	"alpha.dagger.io/random"
)

TestConfig: azureConfig: azure.#Config & {
}

TestSuffix: random.#String & {
	seed: "azst"
}

TestRG: resourcegroup.#ResourceGroup & {
	config:     TestConfig.azureConfig
	rgName:     "rg-test-\(TestSuffix.out)"
	rgLocation: "eastus2"
}

TestStorage: storage.#StorageAccount & {
	config:     TestConfig.azureConfig
	rgName:     "rg-test-ahkkzwyoaucw"
	stLocation: "eastus2"
	stName:     "st\(TestSuffix.out)001"
}
