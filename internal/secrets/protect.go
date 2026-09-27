// Package secrets encrypts OAuth tokens with Windows DPAPI and stores them
// in oauth_tokens. API keys stay in .env; never log token material.
package secrets

import "errors"

// ErrUnsupportedPlatform is returned by Protect/Unprotect on non-Windows builds.
var ErrUnsupportedPlatform = errors.New("secrets: DPAPI Protect/Unprotect only supported on Windows")
