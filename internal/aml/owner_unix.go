//go:build unix

package aml

import (
	"os"
	"syscall"
)

// fileOwner is the uid that owns the file info describes and the process's
// effective uid; ok is false when info has no POSIX owner.
func fileOwner(info os.FileInfo) (owner, process int, ok bool) {
	stat, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return int(stat.Uid), os.Geteuid(), true
}
