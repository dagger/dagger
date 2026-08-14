package signoff

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestImportedTargetComponentsMustMatchTheAdmittedPlan(t *testing.T) {
	t.Parallel()

	digest := func(byte string) string { return "sha256:" + strings.Repeat(byte, 64) }
	observed := TargetComponentIdentities{
		Engine: digest("1"), CLI: digest("2"), GoRuntime: digest("3"),
		RustSDK: digest("4"), RustDescriptor: digest("5"),
	}
	dependency := RustDependencyDescriptor{
		Source: "git", Package: "dagger-sdk", URL: "https://github.com/iw/dagger",
		Revision: strings.Repeat("a", 40),
	}
	dependencyBytes, err := json.Marshal(dependency)
	if err != nil {
		t.Fatal(err)
	}
	dependencyHash := sha256.Sum256(dependencyBytes)
	observed.RustDependency = string(dependencyBytes)
	observed.RustDependencyDigest = fmt.Sprintf("sha256:%x", dependencyHash)
	plan := artifactPlanComponents{
		RustDescriptorDigest:           observed.RustDescriptor,
		RustDependency:                 dependency,
		RustDependencyDescriptorDigest: observed.RustDependencyDigest,
		Components: map[string]struct {
			ContentDigest string `json:"content_digest"`
		}{
			"engine":     {ContentDigest: observed.Engine},
			"cli":        {ContentDigest: observed.CLI},
			"go-runtime": {ContentDigest: observed.GoRuntime},
			"rust-sdk":   {ContentDigest: observed.RustSDK},
		},
	}
	encode := func(value artifactPlanComponents) []byte {
		bytes, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return bytes
	}
	if err := VerifyTargetComponents(encode(plan), observed); err != nil {
		t.Fatalf("matching imported target rejected: %v", err)
	}

	for index, mutation := range []func(*artifactPlanComponents){
		func(value *artifactPlanComponents) {
			record := value.Components["engine"]
			record.ContentDigest = digest("9")
			value.Components["engine"] = record
		},
		func(value *artifactPlanComponents) { value.RustDescriptorDigest = digest("9") },
		func(value *artifactPlanComponents) { value.RustDependency.Revision = strings.Repeat("b", 40) },
		func(value *artifactPlanComponents) { value.RustDependencyDescriptorDigest = digest("9") },
		func(value *artifactPlanComponents) {
			value.Components["unexpected"] = struct {
				ContentDigest string `json:"content_digest"`
			}{ContentDigest: digest("9")}
		},
	} {
		mutated := artifactPlanComponents{
			RustDescriptorDigest:           plan.RustDescriptorDigest,
			RustDependency:                 plan.RustDependency,
			RustDependencyDescriptorDigest: plan.RustDependencyDescriptorDigest,
			Components: make(map[string]struct {
				ContentDigest string `json:"content_digest"`
			}, len(plan.Components)),
		}
		for component, record := range plan.Components {
			mutated.Components[component] = record
		}
		mutation(&mutated)
		if err := VerifyTargetComponents(encode(mutated), observed); err == nil {
			t.Fatalf("component-plan mutation %d was admitted", index)
		}
	}

	mutatedObserved := observed
	mutatedObserved.RustDependency = strings.Replace(
		observed.RustDependency,
		strings.Repeat("a", 40),
		strings.Repeat("b", 40),
		1,
	)
	if err := VerifyTargetComponents(encode(plan), mutatedObserved); err == nil {
		t.Fatal("mutated imported dependency descriptor was admitted")
	}
}
