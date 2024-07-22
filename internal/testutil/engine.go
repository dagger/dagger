package testutil

import (
	"fmt"
	"sync"
)

var (
	nestedEngineCount   uint8
	nestedEngineCountMu sync.Mutex
)

// returns a device name and cidr to use; enables us to have unique devices+ip ranges for nested
// engine services to prevent conflicts
func GetUniqueNestedEngineNetwork() (deviceName string, cidr string) {
	nestedEngineCountMu.Lock()
	defer nestedEngineCountMu.Unlock()

	cur := nestedEngineCount
	nestedEngineCount++
	if nestedEngineCount == 0 {
		panic("nestedEngineCount overflow")
	}

	return fmt.Sprintf("dagger%d", cur), fmt.Sprintf("10.89.%d.0/24", cur)
}
