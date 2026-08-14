package build

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/distconsts"

	"dagger/engine-dev/internal/dagger"
)

func TestWithRustSDKContentRetainsCanonicalDependencyIdentity(t *testing.T) {
	t.Parallel()

	descriptor := `{"source":"git","package":"dagger-sdk","url":"https://github.com/iw/dagger","revision":"0123456789abcdef0123456789abcdef01234567"}`
	digest := sha256.Sum256([]byte(descriptor))
	built, err := new(Builder).WithRustSDKContent(
		new(dagger.Directory),
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64),
		descriptor,
	)
	if err != nil {
		t.Fatalf("retain canonical reusable content: %v", err)
	}
	if got := built.rustSDKContent.extraEnv[distconsts.RustSDKDependencyDescriptorEnvName]; got != descriptor {
		t.Fatalf("dependency descriptor: got %q, want %q", got, descriptor)
	}
	if got, want := built.rustSDKContent.extraEnv[distconsts.RustSDKDependencyDigestEnvName], fmt.Sprintf("sha256:%x", digest); got != want {
		t.Fatalf("dependency descriptor digest: got %q, want %q", got, want)
	}
	if got := built.rustSDKContent.sdkDependency.Revision; got != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("retained dependency revision: got %q", got)
	}
	registryDescriptor := `{"source":"registry","registry":"crates-io","package":"dagger-sdk","exact_version":"1.0.0-beta.10"}`
	registryDigest := sha256.Sum256([]byte(registryDescriptor))
	registry, err := new(Builder).WithRustSDKContent(
		new(dagger.Directory),
		"sha256:"+strings.Repeat("a", 64),
		"sha256:"+strings.Repeat("b", 64),
		registryDescriptor,
	)
	if err != nil {
		t.Fatalf("retain canonical registry content: %v", err)
	}
	if got, want := registry.rustSDKContent.extraEnv[distconsts.RustSDKDependencyDigestEnvName], fmt.Sprintf("sha256:%x", registryDigest); got != want {
		t.Fatalf("registry dependency descriptor digest: got %q, want %q", got, want)
	}
	if got := registry.rustSDKContent.sdkDependency.ExactVersion; got != "1.0.0-beta.10" {
		t.Fatalf("retained registry dependency version: got %q", got)
	}

	for name, invalid := range map[string]string{
		"malformed":          `{"source":`,
		"noncanonical-space": `{ "source":"git","package":"dagger-sdk","url":"https://github.com/iw/dagger","revision":"0123456789abcdef0123456789abcdef01234567" }`,
		"noncanonical-order": `{"package":"dagger-sdk","source":"registry","registry":"crates-io","exact_version":"1.0.0-beta.10"}`,
		"unknown-field":      `{"source":"registry","registry":"crates-io","package":"dagger-sdk","exact_version":"1.0.0-beta.10","extra":true}`,
		"unsupported-source": `{"source":"path","package":"dagger-sdk"}`,
		"mixed-coordinate":   `{"source":"git","registry":"crates-io","package":"dagger-sdk","url":"https://github.com/iw/dagger","revision":"0123456789abcdef0123456789abcdef01234567"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := new(Builder).WithRustSDKContent(
				new(dagger.Directory),
				"sha256:"+strings.Repeat("a", 64),
				"sha256:"+strings.Repeat("b", 64),
				invalid,
			)
			if err == nil {
				t.Fatalf("%s dependency descriptor was admitted", name)
			}
		})
	}
}
