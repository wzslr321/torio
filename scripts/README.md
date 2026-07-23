# Scripts

- `validate_artifacts.py` — portable standard-library validator for required files, repository JSON Schema subset, examples, relative Markdown links and obvious secret patterns.

Run from repository root:

```bash
python3 scripts/validate_artifacts.py
```

The validator is a design-pack consistency check, not a replacement for Go tests, a full secret scanner or runtime security acceptance tests.
