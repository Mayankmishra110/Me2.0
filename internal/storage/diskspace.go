package storage

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// LowDiskThresholdBytes is the free-space alert threshold from ARCHITECTURE
// §8 ("alert if free space < 100 GB") and this ticket's acceptance criteria.
const LowDiskThresholdBytes uint64 = 100 << 30 // 100 GiB

// FreeBytes returns the free space available to the current user on the
// volume containing path (Windows GetDiskFreeSpaceEx). path need not exist
// yet; only its volume is queried.
func FreeBytes(path string) (uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("storage: free bytes: resolve %q: %w", path, err)
	}
	ptr, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return 0, fmt.Errorf("storage: free bytes: encode %q: %w", abs, err)
	}
	var freeAvail, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeAvail, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("storage: free bytes for %q: %w", abs, err)
	}
	return freeAvail, nil
}

// LowDisk reports whether the volume containing path has fewer free bytes
// than LowDiskThresholdBytes, along with the free byte count so the caller
// (the scheduler's storage.cleanup job) can put the number in its alert.
// Delivering the actual Telegram/dashboard alert is out of this package's
// scope; this is the check the alert is built on.
func LowDisk(path string) (low bool, free uint64, err error) {
	free, err = FreeBytes(path)
	if err != nil {
		return false, 0, err
	}
	return free < LowDiskThresholdBytes, free, nil
}
