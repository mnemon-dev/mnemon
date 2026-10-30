//go:build windows

package aml

import "golang.org/x/sys/windows"

// freeDiskBytes is the free space, in bytes, that the calling user may use on
// the volume holding dir.
func freeDiskBytes(dir string) (uint64, error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, nil, nil); err != nil {
		return 0, err
	}
	return free, nil
}
