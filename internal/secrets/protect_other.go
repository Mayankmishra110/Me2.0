//go:build !windows

package secrets

import "fmt"

// Protect is unavailable off Windows; OAuth tokens require DPAPI (ARCHITECTURE §7).
func Protect(plaintext []byte) ([]byte, error) {
	_ = plaintext
	return nil, fmt.Errorf("%w", ErrUnsupportedPlatform)
}

// Unprotect is unavailable off Windows.
func Unprotect(ciphertext []byte) ([]byte, error) {
	_ = ciphertext
	return nil, fmt.Errorf("%w", ErrUnsupportedPlatform)
}
