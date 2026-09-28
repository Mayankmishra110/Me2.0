package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// newTestR2 builds an R2Client pointed at a local httptest server instead of
// real R2 — no real network or credentials are used in these tests.
func newTestR2(t *testing.T, endpoint string) *R2Client {
	t.Helper()
	r2, err := NewR2(R2Config{
		AccountID:       "test-account",
		AccessKeyID:     "test-key",
		SecretAccessKey: "test-secret",
		Bucket:          "test-bucket",
	}, R2Options{EndpointOverride: endpoint})
	if err != nil {
		t.Fatalf("NewR2: %v", err)
	}
	return r2
}

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestNewR2FromEnv_NotConfigured_NoKeys(t *testing.T) {
	_, err := NewR2FromEnv(lookupFrom(nil), R2Options{})
	if err == nil {
		t.Fatal("want error with zero keys")
	}
	if !errors.Is(err, ErrR2NotConfigured) {
		t.Fatalf("err = %v, want errors.Is match on ErrR2NotConfigured", err)
	}
	var nc *NotConfiguredError
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v, want *NotConfiguredError", err)
	}
	want := []string{R2AccountIDEnv, R2AccessKeyIDEnv, R2SecretAccessKeyEnv, R2BucketEnv}
	got := append([]string{}, nc.MissingEnv...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("MissingEnv = %v, want %v", nc.MissingEnv, want)
	}
}

func TestNewR2FromEnv_NotConfigured_PartialKeys(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		missing []string
	}{
		{
			name:    "only account id set",
			env:     map[string]string{R2AccountIDEnv: "acct"},
			missing: []string{R2AccessKeyIDEnv, R2SecretAccessKeyEnv, R2BucketEnv},
		},
		{
			name: "three of four set, bucket blank string counts as missing",
			env: map[string]string{
				R2AccountIDEnv:       "acct",
				R2AccessKeyIDEnv:     "key",
				R2SecretAccessKeyEnv: "secret",
				R2BucketEnv:          "   ", // whitespace-only must count as blank
			},
			missing: []string{R2BucketEnv},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewR2FromEnv(lookupFrom(tc.env), R2Options{})
			var nc *NotConfiguredError
			if !errors.As(err, &nc) {
				t.Fatalf("err = %v, want *NotConfiguredError", err)
			}
			got := append([]string{}, nc.MissingEnv...)
			sort.Strings(got)
			want := append([]string{}, tc.missing...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("MissingEnv = %v, want %v", nc.MissingEnv, want)
			}
		})
	}
}

func TestNewR2FromEnv_AllKeysConfigured(t *testing.T) {
	env := map[string]string{
		R2AccountIDEnv:       "acct",
		R2AccessKeyIDEnv:     "key",
		R2SecretAccessKeyEnv: "secret",
		R2BucketEnv:          "bucket",
	}
	r2, err := NewR2FromEnv(lookupFrom(env), R2Options{})
	if err != nil {
		t.Fatalf("NewR2FromEnv with all keys set: %v", err)
	}
	if r2.Bucket() != "bucket" {
		t.Errorf("Bucket() = %q, want %q", r2.Bucket(), "bucket")
	}
}

func TestNewR2FromEnv_NilLookupDoesNotPanic(t *testing.T) {
	// nil lookup falls back to os.LookupEnv; the real env in a test runner
	// should not have R2 keys set, so this should return NotConfiguredError,
	// not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewR2FromEnv(nil, ...) panicked: %v", r)
		}
	}()
	_, _ = NewR2FromEnv(nil, R2Options{})
}

func TestR2Client_UploadAndDelete(t *testing.T) {
	var gotPut, gotDelete bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			gotPut = true
			w.Header().Set("ETag", `"abc123"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			gotDelete = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	r2 := newTestR2(t, srv.URL)
	ctx := context.Background()

	if err := r2.Upload(ctx, "assets/clip.mp4", strings.NewReader("hello"), 5, "video/mp4"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !gotPut {
		t.Error("server did not see a PUT request")
	}

	if err := r2.Delete(ctx, "assets/clip.mp4"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !gotDelete {
		t.Error("server did not see a DELETE request")
	}
}

func TestR2Client_Upload_RejectsEmptyKey(t *testing.T) {
	r2 := newTestR2(t, "http://127.0.0.1:1") // never dialed; validation happens first
	if err := r2.Upload(context.Background(), "", strings.NewReader("x"), 1, ""); err == nil {
		t.Fatal("want error for empty key")
	}
}

func TestR2Client_PresignGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r2 := newTestR2(t, srv.URL)

	t.Run("default expiry is 1h", func(t *testing.T) {
		rawURL, expiry, err := r2.PresignGET(context.Background(), "assets/clip.mp4", 0)
		if err != nil {
			t.Fatalf("PresignGET: %v", err)
		}
		if expiry != time.Hour {
			t.Errorf("expiry = %v, want 1h", expiry)
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parse presigned url %q: %v", rawURL, err)
		}
		if !strings.HasSuffix(u.Host, strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")) {
			t.Errorf("presigned url host = %q, want to match test server %q", u.Host, srv.URL)
		}
		if !strings.Contains(u.Path, "test-bucket") || !strings.Contains(u.Path, "assets/clip.mp4") {
			t.Errorf("presigned url path = %q, want bucket+key", u.Path)
		}
		q := u.Query()
		if got := q.Get("X-Amz-Expires"); got != "3600" {
			t.Errorf("X-Amz-Expires = %q, want 3600 (1h)", got)
		}
		if q.Get("X-Amz-Signature") == "" {
			t.Error("presigned url is missing a signature")
		}
	})

	t.Run("expiry clamped to the 7 day sigv4 limit", func(t *testing.T) {
		_, expiry, err := r2.PresignGET(context.Background(), "assets/clip.mp4", 30*24*time.Hour)
		if err != nil {
			t.Fatalf("PresignGET: %v", err)
		}
		if expiry != maxPresignExpiry {
			t.Errorf("expiry = %v, want clamped to %v", expiry, maxPresignExpiry)
		}
	})

	t.Run("empty key rejected", func(t *testing.T) {
		if _, _, err := r2.PresignGET(context.Background(), "", time.Hour); err == nil {
			t.Fatal("want error for empty key")
		}
	})
}
