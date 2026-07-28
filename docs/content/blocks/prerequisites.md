## Before you start {#prerequisites}

You need all of the following in place first:

- a macOS host on Apple Silicon with `limactl` on your `PATH` — Torio uses Lima
  `vmType: vz` on `aarch64`, and Intel Macs are out of scope for the guest
  template;
- a checkout of the `torio` repository and a Go toolchain to build the CLI;
- for each repository you plan to attach: read access that already works from
  the guest, without a prompt. Provisioning it is yours to do, outside Torio.

Torio creates the VM itself, so there is nothing to provision by hand first.
