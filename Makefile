.PHONY: validate nvim-smoke e2e platform-e2e fmt fmt-check vet docs docs-check package-release

docs:
	python3 scripts/build_docs.py

docs-check:
	python3 scripts/build_docs.py --check
	python3 scripts/check_site_links.py

validate: docs-check
	python3 scripts/validate_artifacts.py
	python3 -m unittest discover -s scripts -p 'test_*.py'

# Local-only check; CI has no Neovim and nothing else runs this smoke test.
nvim-smoke:
	@if command -v nvim >/dev/null 2>&1; then \
		TORIO_NVIM_ROOT="$(CURDIR)/integrations/neovim" nvim --headless -u NONE -l integrations/neovim/tests/smoke.lua; \
	else \
		echo "nvim not installed; Neovim integration smoke test skipped"; \
	fi

# The e2e suites live in their own module, so every target that touches them
# runs go with -C. Nothing here reaches into the root module and nothing there
# reaches in.
e2e:
	go test -C e2e -count=1 -tags=e2e ./...

# fmt fixes; fmt-check is the CI gate that fails on drift.
fmt:
	@if command -v go >/dev/null 2>&1; then find . -name '*.go' -exec gofmt -w {} +; fi

fmt-check:
	@drift="$$(find . -name '*.go' -exec gofmt -l {} +)"; \
	if [ -n "$$drift" ]; then \
		echo "gofmt drift; run 'make fmt':" >&2; \
		echo "$$drift" >&2; \
		exit 1; \
	fi

vet:
	@if command -v go >/dev/null 2>&1; then go vet ./...; fi
	@if command -v go >/dev/null 2>&1; then go vet -C e2e -tags=e2e ./...; fi
	@if command -v go >/dev/null 2>&1; then go vet -C e2e -tags=platform_e2e ./...; fi

# One journey, run once per backend. PLATFORM_E2E_BACKEND selects which
# (default: hermes); the shared steps are asserted identically on both, which is
# the point of having a backend contract at all.
platform-e2e:
	@test -n "$$PLATFORM_E2E_TORIO_BIN" || (echo "PLATFORM_E2E_TORIO_BIN is required" >&2; exit 2)
	@test -n "$$PLATFORM_E2E_ARTIFACT_DIR" || (echo "PLATFORM_E2E_ARTIFACT_DIR is required" >&2; exit 2)
	PLATFORM_E2E_RUN=1 go test -C e2e -count=1 -tags=platform_e2e -timeout=33m -v \
		./platform -ginkgo.v \
		-ginkgo.label-filter="$$PLATFORM_E2E_LABEL_FILTER" \
		-ginkgo.junit-report="$(abspath $(PLATFORM_E2E_ARTIFACT_DIR))/ginkgo-junit.xml"

# Build + package a release tarball per supported host into dist/.
# Usage: make package-release VERSION=1.0.0
#
# Both hosts land in one dist/ and share a single SHA256SUMS, which
# package_release.py regenerates from the directory on every run. PLATFORMS is
# overridable so the platform-e2e host stage can package only the host it is
# about to install on, instead of cross-building one it will never run.
PLATFORMS ?= darwin/arm64 linux/amd64

package-release:
	@test -n "$(VERSION)" || (echo "VERSION is required, e.g. make package-release VERSION=1.0.0" >&2; exit 2)
	mkdir -p dist
	@set -eu; \
	commit="$$(git rev-parse HEAD)"; \
	date="$$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
	for platform in $(PLATFORMS); do \
		goos="$${platform%%/*}"; goarch="$${platform##*/}"; \
		echo "building $$platform"; \
		GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=0 go build -trimpath \
			-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$$commit -X main.date=$$date" \
			-o "dist/torio-$$goos-$$goarch" ./cmd/torio; \
		python3 scripts/package_release.py --version "$(VERSION)" --platform "$$platform" \
			--binary "dist/torio-$$goos-$$goarch" --license LICENSE --out dist; \
	done
