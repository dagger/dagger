package staticwebapp

import (
	"alpha.dagger.io/azure"
	"alpha.dagger.io/azure/resourcegroup"
	"alpha.dagger.io/azure/staticwebapp"
	"alpha.dagger.io/random"
	"strings"
)

TestConfig: azConfig: azure.#Config & {
}

TestSuffix: random.#String & {
	seed: "azrg"
}

TestRG: resourcegroup.#ResourceGroup & {
	config:     TestConfig.azConfig
	rgName:     "rg-test-\(TestSuffix.out)"
	rgLocation: "eastus2"
}

// rgName is obtained from above TestRG
TestSWA: staticwebapp.#StaticWebApp & {
	config:        TestRG.config
	rgName:        "\(strings.Split(TestRG.id, "/")[4])"
	stappLocation: "eastus2"
	stappName:     "stapp-test-\(TestSuffix.out)"
	remote:        "https://github.com/sujaypillai/todoapp"
}
