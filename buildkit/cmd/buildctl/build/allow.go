package build

import (
	"github.com/moby/buildkit/util/entitlements"
)

// ParseAllow parses --allow
func ParseAllow(inp []string) ([]entitlements.Entitlement, error) {
	ent := make([]entitlements.Entitlement, 0, len(inp))
	for _, v := range inp {
		e, err := entitlements.Parse(v)
		if err != nil {
			return nil, err
		}
		ent = append(ent, e)
	}
	return ent, nil
}
