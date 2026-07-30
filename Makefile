.PHONY: validate test fmt vet docs docs-check package-release

docs:
	python3 scripts/build_docs.py

docs-check:
	python3 scripts/build_docs.py --check
	python3 scripts/check_site_links.py

validate: docs-check
	python3 scripts/validate_artifacts.py
	python3 -m unittest discover -s scripts -p 'test_*.py'
	bash spikes/v1-e2e/run_test.sh

test: validate
	@if command -v go >/dev/null 2>&1; then go test ./...; else echo "go not installed; documentation validation completed"; fi
	@if command -v nvim >/dev/null 2>&1; then \
		TORIO_NVIM_ROOT="$(CURDIR)/integrations/neovim" nvim --headless -u NONE -l integrations/neovim/tests/smoke.lua; \
	else \
		echo "nvim not installed; Neovim integration smoke test skipped"; \
	fi

fmt:
	@if command -v go >/dev/null 2>&1; then gofmt -w $$(find . -name '*.go' -not -path './.worktrees/*'); fi

vet:
	@if command -v go >/dev/null 2>&1; then go vet ./...; fi

# Build + package a darwin/arm64 release tarball into dist/.
# Usage: make package-release VERSION=1.0.0
package-release:
	@test -n "$(VERSION)" || (echo "VERSION is required, e.g. make package-release VERSION=1.0.0" >&2; exit 2)
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$$(git rev-parse HEAD) -X main.date=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o dist/torio ./cmd/torio
	python3 scripts/package_release.py --version "$(VERSION)" --binary dist/torio --license LICENSE --out dist
