package cloud

import (
	"context"
	"time"
)

// BillingUsage is the saved billing estimate of an org for a UTC calendar
// month, as shown on the Billing & usage page of Dagger Cloud. The API also
// exposes daily quantities, which the CLI does not request.
type BillingUsage struct {
	Month string `json:"month"`
	// Revision is zero until the month is calculated for the first time.
	Revision int64 `json:"revision"`
	// Calculation is the source of the quantities: "legacy" or "workload".
	Calculation string     `json:"calculation"`
	AsOf        *time.Time `json:"asOf"`
	// ProcessedThrough is set for workload results: work that ended after it
	// can be missing.
	ProcessedThrough *time.Time           `json:"processedThrough"`
	CalculatedAt     *time.Time           `json:"calculatedAt"`
	RefreshStatus    string               `json:"refreshStatus"`
	RefreshMonth     string               `json:"refreshMonth"`
	NextRefreshAt    *time.Time           `json:"nextRefreshAt"`
	Error            string               `json:"error"`
	FinalizedAt      *time.Time           `json:"finalizedAt"`
	InvoiceID        *string              `json:"invoiceID"`
	InvoiceURL       *string              `json:"invoiceURL"`
	Warnings         []string             `json:"warnings"`
	Pricing          *BillingUsagePricing `json:"pricing"`
	Bill             *BillingUsageBill    `json:"bill"`
}

type BillingUsagePricing struct {
	PlanID     string  `json:"planID"`
	Name       string  `json:"name"`
	MinimumUSD float64 `json:"minimumUSD"`
}

type BillingUsageBill struct {
	UsageUSD             float64                `json:"usageUSD"`
	MinimumAdjustmentUSD float64                `json:"minimumAdjustmentUSD"`
	TotalUSD             float64                `json:"totalUSD"`
	Resources            []BillingUsageResource `json:"resources"`
}

// BillingUsageResource is the bill of one resource (CPU, Memory, Inputs or
// Outputs). Quantities and prices use Unit (CPU-hours, GB-hours or GB).
type BillingUsageResource struct {
	Resource        string  `json:"resource"`
	Unit            string  `json:"unit"`
	NewQuantity     float64 `json:"newQuantity"`
	RepeatRan       float64 `json:"repeatRan"`
	RepeatCached    float64 `json:"repeatCached"`
	RepeatEstimated float64 `json:"repeatEstimated"`
	NewPrice        float64 `json:"newPrice"`
	RepeatPrice     float64 `json:"repeatPrice"`
	NewChargeUSD    float64 `json:"newChargeUSD"`
	RepeatChargeUSD float64 `json:"repeatChargeUSD"`
	Estimated       bool    `json:"estimated"`
}

const billingUsageOperation = `
query GetBillingUsage($org: ID!, $month: String!) {
	billingUsage(org: $org, month: $month) {
		month revision calculation asOf processedThrough calculatedAt
		refreshStatus refreshMonth nextRefreshAt error
		finalizedAt invoiceID invoiceURL warnings
		pricing { planID name minimumUSD }
		bill {
			usageUSD minimumAdjustmentUSD totalUSD
			resources {
				resource unit newQuantity repeatRan repeatCached repeatEstimated
				newPrice repeatPrice newChargeUSD repeatChargeUSD estimated
			}
		}
	}
}
`

// BillingUsage returns the saved billing estimate of the org for the UTC month
// (YYYY-MM). Reading does not start a calculation.
func (c *Client) BillingUsage(ctx context.Context, orgID, month string) (*BillingUsage, error) {
	vars := map[string]any{
		"org":   orgID,
		"month": month,
	}
	var data struct {
		BillingUsage BillingUsage `json:"billingUsage"`
	}
	if err := c.doGraphQL(ctx, "GetBillingUsage", billingUsageOperation, vars, &data); err != nil {
		return nil, err
	}
	return &data.BillingUsage, nil
}
