package signoff

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// TargetComponentIdentities are independently observed from one imported target container.
type TargetComponentIdentities struct {
	Engine               string
	CLI                  string
	GoRuntime            string
	RustSDK              string
	RustDescriptor       string
	RustDependency       string
	RustDependencyDigest string
}

// RustDependencyDescriptor is the compact packaged coordinate emitted by engine-dev and
// independently decoded from an imported target. Its field order is the byte-level format.
type RustDependencyDescriptor struct {
	Source   string `json:"source"`
	Package  string `json:"package"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
}

type artifactPlanComponents struct {
	RustDescriptorDigest           string                   `json:"rust_descriptor_digest"`
	RustDependency                 RustDependencyDescriptor `json:"rust_dependency"`
	RustDependencyDescriptorDigest string                   `json:"rust_dependency_descriptor_digest"`
	Components                     map[string]struct {
		ContentDigest string `json:"content_digest"`
	} `json:"components"`
}

// VerifyTargetComponents rejects an imported target whose runtime bytes or embedded SDK
// identities differ from the admitted artifact plan.
func VerifyTargetComponents(planJSON []byte, observed TargetComponentIdentities) error {
	var plan artifactPlanComponents
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return fmt.Errorf("decode admitted exact-target component plan: %w", err)
	}
	expected := map[string]string{
		"engine":     plan.Components["engine"].ContentDigest,
		"cli":        plan.Components["cli"].ContentDigest,
		"go-runtime": plan.Components["go-runtime"].ContentDigest,
		"rust-sdk":   plan.Components["rust-sdk"].ContentDigest,
	}
	actual := map[string]string{
		"engine":     observed.Engine,
		"cli":        observed.CLI,
		"go-runtime": observed.GoRuntime,
		"rust-sdk":   observed.RustSDK,
	}
	var observedDependency RustDependencyDescriptor
	if err := json.Unmarshal([]byte(observed.RustDependency), &observedDependency); err != nil {
		return fmt.Errorf("decode imported exact-target Rust dependency descriptor: %w", err)
	}
	canonicalDependency, err := json.Marshal(observedDependency)
	if err != nil || string(canonicalDependency) != observed.RustDependency {
		return fmt.Errorf("imported exact-target Rust dependency descriptor is not canonical")
	}
	dependencyDigest := sha256.Sum256(canonicalDependency)
	observedDependencyDigest := fmt.Sprintf("sha256:%x", dependencyDigest)
	if len(plan.Components) != len(expected) || plan.RustDescriptorDigest != observed.RustDescriptor ||
		plan.RustDependency != observedDependency ||
		plan.RustDependencyDescriptorDigest != observed.RustDependencyDigest ||
		observed.RustDependencyDigest != observedDependencyDigest {
		return fmt.Errorf("imported exact-target descriptor differs from its admitted plan")
	}
	for component, digest := range expected {
		if !canonicalSHA256(digest) || digest != actual[component] {
			return fmt.Errorf("imported exact-target %s bytes differ from the admitted plan", component)
		}
	}
	return nil
}

func canonicalSHA256(value string) bool {
	if len(value) != 71 || value[:7] != "sha256:" {
		return false
	}
	for _, character := range value[7:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
