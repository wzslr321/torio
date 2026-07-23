.PHONY: validate test fmt vet

validate:
	python3 scripts/validate_artifacts.py

test: validate
	@if command -v go >/dev/null 2>&1; then go test ./...; else echo "go not installed; documentation validation completed"; fi

fmt:
	@if command -v go >/dev/null 2>&1; then gofmt -w $$(find . -name '*.go' -not -path './.worktrees/*'); fi

vet:
	@if command -v go >/dev/null 2>&1; then go vet ./...; fi
