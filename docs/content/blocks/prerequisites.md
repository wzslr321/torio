## Before you start {#prerequisites}

You need all of the following in place first:

- a macOS host on Apple Silicon with `limactl` on your `PATH` (Torio V1 uses Lima
  `vmType: vz` on `aarch64`; Intel Macs are out of scope for the product template);
- the `torio` Lima VM — V1 may create it via `torio vm init`; the legacy V0 path
  may use an already-provisioned instance instead;
- a checkout of the `torio` repository and a Go toolchain to build the CLI;
- for the Code V0 workspace only: repo-scoped **read** access to the private remote must already exist on the guest. Provisioning it is a human-only prerequisite outside Torio.
