//go:build platform_e2e

package platform

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlatformE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Real Lima Platform E2E Suite")
}
