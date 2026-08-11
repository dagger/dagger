package netinst

import (
	"context"
	"encoding/json"
	"testing"
)

func ipamRanges(t *testing.T, cfg []byte) []any {
	t.Helper()
	var parsed struct {
		Plugins []struct {
			Type string `json:"type"`
			IPAM struct {
				Ranges []any `json:"ranges"`
			} `json:"ipam"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("unmarshal cni config: %v", err)
	}
	for _, p := range parsed.Plugins {
		if p.Type == "bridge" {
			return p.IPAM.Ranges
		}
	}
	t.Fatal("no bridge plugin in generated cni config")
	return nil
}

func TestCNIConfigRanges(t *testing.T) {
	for _, tc := range []struct {
		name       string
		subnets    []string
		wantRanges int
	}{
		{"ipv4 only", []string{"10.87.0.0/16"}, 1},
		{"dual stack", []string{"10.87.0.0/16", "fdaa:dag::/64"}, 2},
		{"empty ipv6 is skipped", []string{"10.87.0.0/16", ""}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := cniConfig(context.Background(), "dagger", tc.subnets...)
			if err != nil {
				t.Fatalf("cniConfig: %v", err)
			}
			if got := len(ipamRanges(t, cfg)); got != tc.wantRanges {
				t.Errorf("ipam ranges = %d, want %d", got, tc.wantRanges)
			}
		})
	}
}
