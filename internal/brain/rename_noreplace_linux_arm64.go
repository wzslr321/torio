//go:build linux && arm64

package brain

// Linux arm64 __NR_renameat2 from the pinned Go toolchain's generated syscall
// table.
const renameat2Trap = 276
