//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func diskFreeBytes(path string) (free, total uint64, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, 0, fmt.Errorf("abs %s: %w", path, err)
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		return 0, 0, fmt.Errorf("no volume for %s", abs)
	}
	root := vol + `\`
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, 0, fmt.Errorf("utf16 %s: %w", root, err)
	}
	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(rootPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx(%s): %w", root, err)
	}
	return freeBytesAvailable, totalBytes, nil
}

func totalRAMBytes() (uint64, error) {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	if err := globalMemoryStatusEx(&status); err != nil {
		return 0, err
	}
	return status.TotalPhys, nil
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func globalMemoryStatusEx(status *memoryStatusEx) error {
	mod := windows.NewLazySystemDLL("kernel32.dll")
	proc := mod.NewProc("GlobalMemoryStatusEx")
	r1, _, e1 := proc.Call(uintptr(unsafe.Pointer(status)))
	if r1 == 0 {
		if e1 != nil {
			return fmt.Errorf("GlobalMemoryStatusEx: %w", e1)
		}
		return fmt.Errorf("GlobalMemoryStatusEx failed")
	}
	return nil
}
