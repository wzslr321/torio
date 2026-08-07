---
applyTo: "**/*_test.go"
---

# Go tests

Every behaviour change is RED then GREEN. A production diff with no test that
would have failed before it is a finding, and so is a test that would pass
against the old code.

## What a test has to prove

- Enforcement, not configuration. Asserting that a field was set, a flag was
  passed or a file was written is not proof that the refusal happens. Assert the
  observable outcome: the error, the exit code, the envelope, the absent side
  effect.
- The negative case. A test that only covers the happy path leaves the
  fail-closed claim unproven.
- Cancellation and timeout behaviour where the code under test runs a command.

## What a test must not do

- Reach the network, a real `limactl`, a real VM, or the developer's home
  directory. `go test ./...` runs with no host support. Use the package fakes
  and `t.TempDir()`.
- Carry a credential, token, private key or real hostname in a fixture. Write
  `[REDACTED]` or an obviously synthetic value.
- Depend on another test's state or on execution order.
- Assert on a log string as if it were the contract, unless the test is about
  redaction.

## Shape

- Subtests are named for the behaviour they pin, not `case1`.
- Prefer a table when the cases differ only in input, and separate tests when
  they differ in what is being proven.
- Changes to concurrency or shared state need `go test -race ./...` before
  review, and the pull request should say it was run.
