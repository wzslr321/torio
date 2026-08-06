## Before you start {#prerequisites}

You need all of the following in place first:

- a supported host with `limactl` on your `PATH`: macOS on Apple Silicon, where
  Torio uses Lima `vmType: vz` on `aarch64`, or Linux on x86_64, where it uses
  `qemu` on `x86_64` over KVM. Intel Macs are out of scope — `vz` needs Apple
  Silicon — and so is arm64 Linux, which nothing here has booted;
- a checkout of the `torio` repository and a Go toolchain to build the CLI;
- for each repository you plan to attach: read access that already works from
  the guest, without a prompt. Provisioning it is yours to do, outside Torio.

Torio creates the VM itself, so there is nothing to provision by hand first.
