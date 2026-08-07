---
applyTo: "e2e/**"
---

# End-to-end suites

`e2e/` is its own Go module, and that separation is deliberate: it stops a test
framework's dependency graph from reaching the module that holds the credential
boundary.

## Flag

- Any import of the product's `internal/…` packages. Both suites drive the
  compiled `torio` binary as a separate process and assert on its JSON
  envelopes, exit codes and side effects. Reaching into the code under test
  defeats the point of the suite.
- A dependency added to the root `go.mod` because a test needed it.
- A test that drops its build tag. `make e2e` is tag `e2e`, `make platform-e2e`
  is tag `platform_e2e`, and neither runs unless it is asked for.
- A `platform_e2e` test that leaves an instance behind on failure, or that
  targets an instance name not derived from the run.
- A Ginkgo spec placed on the wrong side of the `host` and `guest` labels.
  `host` covers the release tarball, the installer, real `limactl` and `vm
  init`, and runs anywhere. `guest` covers everything from `vm start` on and
  needs a usable hypervisor.

## Prefer

- Assertions on the envelope and the exit code over assertions on log text.
- Re-running a mutating step in the same spec to prove idempotence, where the
  command claims it.
