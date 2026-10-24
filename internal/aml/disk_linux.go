//go:build linux

package aml

import "syscall"

// freeDiskBytes is the free space, in bytes, that a process without special
// privileges may use on the file system holding dir. Linux counts blocks in
// fragments (Frsize), which on some file systems differ from the block size.
func freeDiskBytes(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	unit := uint64(stat.Frsize)
	if unit == 0 {
		unit = uint64(stat.Bsize)
	}
	return uint64(stat.Bavail) * unit, nil
}
