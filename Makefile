.PHONY: validate test fmt vet docs docs-check

docs:
	python3 scripts/build_docs.py

docs-check:
	python3 scripts/build_docs.py --check
	python3 scripts/check_site_links.py

validate: docs-check
	python3 scripts/validate_artifacts.py
	python3 -m unittest discover -s scripts -p 'test_*.py'

test: validate
	@if command -v go >/dev/null 2>&1; then go test ./...; else echo "go not installed; documentation validation completed"; fi

fmt:
	@if command -v go >/dev/null 2>&1; then gofmt -w $$(find . -name '*.go' -not -path './.worktrees/*'); fi

vet:
	@if command -v go >/dev/null 2>&1; then go vet ./...; fi
