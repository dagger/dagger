package version_test

import (
	"fmt"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.dagger.io/dagger/version"
)

var _ = Describe("Version", func() {
	Describe("Short()", func() {
		It("prints version and revision", func() {
			Expect(version.Short()).To(Equal("dagger devel ()"))
		})
	})
	Describe("Long()", func() {
		It("prints version, revision, os & platform", func() {
			longVersionOutput := fmt.Sprintf("dagger devel () %s/%s", runtime.GOOS, runtime.GOARCH)
			Expect(version.Long()).To(Equal(longVersionOutput))
		})
	})
})
