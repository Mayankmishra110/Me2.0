//go:build windows

package secrets

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// description labels the ciphertext inside the DPAPI blob (visible in some
// Windows tooling; not a secret).
const description = "mayank2-oauth"

// Protect encrypts plaintext with the current Windows user's DPAPI master key
// (CryptProtectData). The result can only be decrypted by the same user on
// this machine. Empty input returns empty output (CryptProtectData rejects
// zero-length blobs).
func Protect(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	in, err := newBlob(plaintext)
	if err != nil {
		return nil, err
	}
	var out windows.DataBlob
	name, err := windows.UTF16PtrFromString(description)
	if err != nil {
		return nil, fmt.Errorf("secrets.Protect: description: %w", err)
	}
	if err := windows.CryptProtectData(
		&in,
		name,
		nil,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&out,
	); err != nil {
		return nil, fmt.Errorf("secrets.Protect: CryptProtectData: %w", err)
	}
	defer freeBlob(out)
	return blobBytes(out), nil
}

// Unprotect decrypts a blob previously produced by Protect.
func Unprotect(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	in, err := newBlob(ciphertext)
	if err != nil {
		return nil, err
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(
		&in,
		nil,
		nil,
		0,
		nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN,
		&out,
	); err != nil {
		return nil, fmt.Errorf("secrets.Unprotect: CryptUnprotectData: %w", err)
	}
	defer freeBlob(out)
	return blobBytes(out), nil
}

func newBlob(b []byte) (windows.DataBlob, error) {
	if len(b) == 0 {
		return windows.DataBlob{}, nil
	}
	if len(b) > int(^uint32(0)) {
		return windows.DataBlob{}, fmt.Errorf("secrets: blob too large (%d bytes)", len(b))
	}
	return windows.DataBlob{
		Size: uint32(len(b)),
		Data: &b[0],
	}, nil
}

func blobBytes(b windows.DataBlob) []byte {
	if b.Size == 0 || b.Data == nil {
		return nil
	}
	out := make([]byte, b.Size)
	copy(out, unsafe.Slice(b.Data, b.Size))
	return out
}

func freeBlob(b windows.DataBlob) {
	if b.Data == nil {
		return
	}
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data)))
}
