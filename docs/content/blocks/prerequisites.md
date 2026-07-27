## Before you start {#prerequisites}

You need all of the following in place first:

- a macOS host on Apple Silicon with `limactl` on your `PATH`;
- the `torio` Lima VM **already created** — Torio never creates, re-images, or destroys it;
- a checkout of the `torio` repository and a Go toolchain to build the CLI;
- for the Code V0 workspace only: repo-scoped **read** access to the private remote must already exist on the guest. Provisioning it is a human-only prerequisite outside Torio.
