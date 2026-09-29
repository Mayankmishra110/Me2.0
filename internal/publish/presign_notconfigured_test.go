package publish

import (
	"context"
	"strings"
	"testing"
	"time"
)

// notConfiguredPresign mirrors cmd/mayank2/run.go's notConfiguredPresigner
// (M2-122): what registerPublishHandlers wires into Instagram/Facebook/
// Pinterest's Presign field when R2 isn't configured (CONTEXT D24). Unlike
// a nil Presign, this is a real, non-nil implementation that still returns
// a clean, typed error from PresignGET — proving the "not configured" path
// works whether or not each publisher's own `if x.Presign == nil` guard is
// present, and giving the regression test something to assert never panics
// even when it does get called.
type notConfiguredPresign struct{}

func (notConfiguredPresign) PresignGET(ctx context.Context, key string, expiry time.Duration) (string, time.Duration, error) {
	return "", 0, errNotConfiguredPresign
}

var errNotConfiguredPresign = &notConfiguredPresignError{}

type notConfiguredPresignError struct{}

func (*notConfiguredPresignError) Error() string {
	return "r2 not configured: set R2_ACCOUNT_ID, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, R2_BUCKET in .env"
}

// TestInstagram_PresignNotConfigured_ReturnsCleanError is the M2-122
// regression test: before this ticket, cmd/mayank2/run.go never assigned
// Instagram.Presign at all (left nil), and while resolvePublicURL's own
// `i.Presign == nil` guard already prevented a nil-interface-method panic,
// nothing proved that end-to-end. This constructs Instagram exactly as it
// would be wired with R2 unconfigured (a non-nil Presign that errors) and
// confirms Publish returns a normal error — never panics, never a zero/ok
// result — regardless of which guard (the nil check or the presigner
// itself) is the one that actually stops it.
func TestInstagram_PresignNotConfigured_ReturnsCleanError(t *testing.T) {
	ig := seedIGPub(t, "c-ig-nc", "ig-biz-nc", "pub-ig-nc", "c-ig-nc:instagram:ig-biz-nc")
	ig.Presign = notConfiguredPresign{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Publish panicked with unconfigured Presign: %v", r)
		}
	}()
	_, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID:  "pub-ig-nc",
		ContentID:      "c-ig-nc",
		Account:        "ig-biz-nc",
		IdempotencyKey: "c-ig-nc:instagram:ig-biz-nc",
		VideoPath:      "renders/short.mp4",
		Description:    "x",
	})
	if err == nil {
		t.Fatal("want error when R2 not configured, got nil")
	}
	if !strings.Contains(err.Error(), "r2 not configured") {
		t.Fatalf("want r2-not-configured error, got: %v", err)
	}
}

// TestInstagram_NilPresign_ReturnsCleanError is the belt-and-suspenders
// case: a genuinely nil Presign (the pre-M2-122 zero value) must also
// never panic — resolvePublicURL's own guard is the thing that catches it
// if buildPresigner is ever accidentally skipped.
func TestInstagram_NilPresign_ReturnsCleanError(t *testing.T) {
	ig := seedIGPub(t, "c-ig-nil", "ig-biz-nil", "pub-ig-nil", "c-ig-nil:instagram:ig-biz-nil")
	ig.Presign = nil

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Publish panicked with nil Presign: %v", r)
		}
	}()
	_, err := ig.Publish(context.Background(), PublishRequest{
		PublicationID:  "pub-ig-nil",
		ContentID:      "c-ig-nil",
		Account:        "ig-biz-nil",
		IdempotencyKey: "c-ig-nil:instagram:ig-biz-nil",
		VideoPath:      "renders/short.mp4",
		Description:    "x",
	})
	if err == nil {
		t.Fatal("want error when Presign is nil, got nil")
	}
}

func TestFacebook_PresignNotConfigured_ReturnsCleanError(t *testing.T) {
	fb := seedFB(t, "c-fb-nc", "page-nc", "pub-fb-nc", "c-fb-nc:facebook:page-nc")
	fb.Presign = notConfiguredPresign{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Publish panicked with unconfigured Presign: %v", r)
		}
	}()
	_, err := fb.Publish(context.Background(), PublishRequest{
		PublicationID:  "pub-fb-nc",
		ContentID:      "c-fb-nc",
		Account:        "page-nc",
		IdempotencyKey: "c-fb-nc:facebook:page-nc",
		VideoPath:      "renders/a.mp4",
		Title:          "T",
		Description:    "D",
	})
	if err == nil {
		t.Fatal("want error when R2 not configured, got nil")
	}
	if !strings.Contains(err.Error(), "r2 not configured") {
		t.Fatalf("want r2-not-configured error, got: %v", err)
	}
}

func TestPinterest_PresignNotConfigured_ReturnsCleanError(t *testing.T) {
	pin, video := seedPin(t, "c-p-nc", "pin-acct-nc", "pub-p-nc", "c-p-nc:pinterest:pin-acct-nc")
	pin.Presign = notConfiguredPresign{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Publish panicked with unconfigured Presign: %v", r)
		}
	}()
	_, err := pin.Publish(context.Background(), PublishRequest{
		PublicationID:  "pub-p-nc",
		ContentID:      "c-p-nc",
		Account:        "pin-acct-nc",
		IdempotencyKey: "c-p-nc:pinterest:pin-acct-nc",
		VideoPath:      video,
		ThumbnailPath:  "renders/cover.png",
		PlaylistID:     "board-1", // Pinterest reuses PlaylistID as the board ID
		Title:          "T",
		Description:    "D",
	})
	if err == nil {
		t.Fatal("want error when R2 not configured, got nil")
	}
	if !strings.Contains(err.Error(), "r2 not configured") {
		t.Fatalf("want r2-not-configured error, got: %v", err)
	}
}
