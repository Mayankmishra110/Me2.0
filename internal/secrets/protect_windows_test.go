//go:build windows

package secrets

import (
	"bytes"
	"testing"
)

func TestProtectUnprotectRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{name: "empty", in: nil},
		{name: "short", in: []byte("hello")},
		{name: "json-ish", in: []byte(`{"access_token":"x","refresh_token":"y"}`)},
		{name: "binary", in: []byte{0x00, 0xff, 0x01, 0xfe}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cipher, err := Protect(tc.in)
			if err != nil {
				t.Fatalf("Protect: %v", err)
			}
			if len(tc.in) > 0 && bytes.Equal(cipher, tc.in) {
				t.Fatal("Protect returned plaintext unchanged")
			}
			plain, err := Unprotect(cipher)
			if err != nil {
				t.Fatalf("Unprotect: %v", err)
			}
			if !bytes.Equal(plain, tc.in) {
				t.Fatalf("round-trip mismatch: got %q want %q", plain, tc.in)
			}
		})
	}
}

func TestUnprotectGarbage(t *testing.T) {
	_, err := Unprotect([]byte("not-a-dpapi-blob"))
	if err == nil {
		t.Fatal("expected error for garbage ciphertext")
	}
}
