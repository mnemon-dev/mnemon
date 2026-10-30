//go:build !(darwin || freebsd || dragonfly || linux || windows)

package aml

import (
	"errors"
	"runtime"
)

// freeDiskBytes measures nothing on a system whose free space the server does
// not know how to read (the releases are linux, darwin and windows): readiness
// then reports the free disk unreadable.
func freeDiskBytes(string) (uint64, error) {
	return 0, errors.New("free disk space is not measured on " + runtime.GOOS)
}
