//go:build !windows && !linux

package aml

import "syscall"

// freeDiskBytes is the free space, in bytes, that a process without special
// privileges may use on the file system holding dir.
func freeDiskBytes(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
