//go:build linux && amd64

package brain

// Linux amd64 __NR_renameat2 from the pinned Go toolchain's generated syscall
// table. The standard syscall package does not export it on this architecture.
const renameat2Trap = 316
