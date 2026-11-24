package modelcatalog

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
)

func TestCopilotCostUsesCatalogPricing(t *testing.T) {
	load()
	models := catalog[catwalk.InferenceProviderCopilot]
	const modelID = "copilot-cost-regression"
	models[modelID] = catwalk.Model{
		ID:                 modelID,
		CostPer1MIn:        1,
		CostPer1MOut:       2,
		CostPer1MInCached:  3,
		CostPer1MOutCached: 4,
	}
	t.Cleanup(func() {
		delete(models, modelID)
	})

	const million = 1_000_000
	if got := Cost("github-copilot", modelID, million, million, million, million); got != 10 {
		t.Fatalf("Copilot catalog cost = %v, want 10", got)
	}
}
