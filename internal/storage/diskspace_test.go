package storage

import "testing"

func TestFreeBytes_RealPath(t *testing.T) {
	free, err := FreeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("FreeBytes: %v", err)
	}
	if free == 0 {
		t.Error("FreeBytes = 0, want a plausible positive value for a real, writable path")
	}
}

func TestLowDisk(t *testing.T) {
	// A real temp dir should not be within 100 GB of full on any dev/CI
	// machine this runs on; this is a smoke test of the wiring, not a
	// guarantee about the host's actual free space.
	low, free, err := LowDisk(t.TempDir())
	if err != nil {
		t.Fatalf("LowDisk: %v", err)
	}
	if free == 0 {
		t.Error("free = 0, want > 0")
	}
	wantLow := free < LowDiskThresholdBytes
	if low != wantLow {
		t.Errorf("low = %v, want %v (free=%d threshold=%d)", low, wantLow, free, LowDiskThresholdBytes)
	}
}

func TestLowDiskThresholdBytes_Value(t *testing.T) {
	const gib = 1 << 30
	if LowDiskThresholdBytes != 100*gib {
		t.Errorf("LowDiskThresholdBytes = %d, want %d (100 GiB)", LowDiskThresholdBytes, 100*gib)
	}
}
