//go:build !windows

package secrets

import (
	"errors"
	"testing"
)

func TestProtectOtherPlatform(t *testing.T) {
	_, err := Protect([]byte("x"))
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Protect: got %v want ErrUnsupportedPlatform", err)
	}
	_, err = Unprotect([]byte("x"))
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Unprotect: got %v want ErrUnsupportedPlatform", err)
	}
}
