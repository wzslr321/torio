---
applyTo: "internal/cli/**,docs/contracts/**,cmd/**"
---

# CLI surface and contracts

`docs/contracts/cli.md` and `docs/contracts/config.md` describe the delivered
binary. A disagreement between one of them and the code is a defect to fix in
one of the two, never a state to accept.

## Output

- Machine output is the stable envelope in `internal/cli/envelope.go`, with its
  `schema_version`. Hand-rolled JSON, a bare `fmt.Printf` of a struct, or a new
  top-level field added without a contract change is a finding.
- Human text goes to stderr. Machine output goes to stdout. Mixing them breaks
  every caller that parses the envelope.
- Errors are built with the helpers in `internal/cli/exit.go` and their details
  pass through the redaction step. A raw error wrapped straight into the
  envelope can leak a path, an argv or a remote.

## Exit codes

- Exit codes come from the `ExitCode` constants. A new numeric literal is a
  finding.
- A new code, or a changed meaning for an existing one, must land in the
  contract in the same pull request.

## Coverage

- A new command, subcommand or flag must appear in `docs/contracts/cli.md`.
  `make validate` fails otherwise, so an undocumented one is a broken build, not
  a style preference.
- Operator-visible strings carry no internal milestone label. No "V0", no "V1".
  The operator reads the version from `torio version`.
- Help text and the contract state the same defaults. A default changed in one
  place only is a finding.
