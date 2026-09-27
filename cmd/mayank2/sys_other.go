//go:build !windows

package main

import "fmt"

func diskFreeBytes(path string) (free, total uint64, err error) {
	return 0, 0, fmt.Errorf("disk free check not implemented on this OS (path=%s)", path)
}

func totalRAMBytes() (uint64, error) {
	return 0, fmt.Errorf("RAM check not implemented on this OS")
}
