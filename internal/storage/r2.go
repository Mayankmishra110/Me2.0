package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Env keys that enable Cloudflare R2 (D24, .env.example). All four must be
// set and non-blank, or storage falls back to local-only (never panics).
const (
	R2AccountIDEnv       = "R2_ACCOUNT_ID"
	R2AccessKeyIDEnv     = "R2_ACCESS_KEY_ID"
	R2SecretAccessKeyEnv = "R2_SECRET_ACCESS_KEY"
	R2BucketEnv          = "R2_BUCKET"
	defaultPresignExpiry = time.Hour // CONTEXT D20 / ticket: presigned GET URLs are 1h
	minPresignExpiry     = time.Minute
	maxPresignExpiry     = 7 * 24 * time.Hour // S3 SigV4 presign hard limit
)

// ErrR2NotConfigured is matched (errors.Is) by *NotConfiguredError.
var ErrR2NotConfigured = errors.New("r2 not configured")

// NotConfiguredError is returned when one or more of the four required R2
// env keys is missing or blank. It is a typed, non-panicking result: content
// that only ever runs local-only storage never sees R2 code at all, and
// content with a partial key set gets a clear, specific error instead of a
// crash or a silent no-op.
type NotConfiguredError struct {
	// MissingEnv lists the env key names that must be set to enable R2.
	// Names only — values (even partial ones) are never included or logged.
	MissingEnv []string
}

func (e *NotConfiguredError) Error() string {
	return fmt.Sprintf("r2 not configured: set %s in .env", strings.Join(e.MissingEnv, ", "))
}

// Is lets errors.Is(err, ErrR2NotConfigured) match.
func (e *NotConfiguredError) Is(target error) bool { return target == ErrR2NotConfigured }

// R2Config names the four required settings (D24). All fields are required;
// use NewR2FromEnv to build one from the process environment with a proper
// NotConfiguredError on any gap.
type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

func (c R2Config) missing() []string {
	var missing []string
	if strings.TrimSpace(c.AccountID) == "" {
		missing = append(missing, R2AccountIDEnv)
	}
	if strings.TrimSpace(c.AccessKeyID) == "" {
		missing = append(missing, R2AccessKeyIDEnv)
	}
	if strings.TrimSpace(c.SecretAccessKey) == "" {
		missing = append(missing, R2SecretAccessKeyEnv)
	}
	if strings.TrimSpace(c.Bucket) == "" {
		missing = append(missing, R2BucketEnv)
	}
	return missing
}

// R2Options lets tests point the client at a fake S3-compatible endpoint
// instead of real R2, and override the HTTP client. Zero value talks to real
// Cloudflare R2.
type R2Options struct {
	// EndpointOverride, when set, replaces the default
	// https://<account-id>.r2.cloudflarestorage.com endpoint. Tests point
	// this at an httptest server; production leaves it empty.
	EndpointOverride string
	HTTPClient       *http.Client
}

// R2Client uploads to and presigns/deletes objects in one Cloudflare R2
// bucket via the S3-compatible API (aws-sdk-go-v2 s3, per the README stack
// table).
type R2Client struct {
	bucket  string
	client  *s3.Client
	presign *s3.PresignClient
}

// NewR2FromEnv builds an R2Client from the four R2_* env vars (D24). lookup
// is usually os.LookupEnv (after config.LoadEnvFile loads .env); a nil lookup
// defaults to os.LookupEnv. Any of the four keys missing or blank returns
// *NotConfiguredError naming exactly the missing ones — never a panic, and
// callers must treat this as "R2 disabled", not a fatal error, so local-only
// storage keeps working with zero R2 keys.
func NewR2FromEnv(lookup func(string) (string, bool), opts R2Options) (*R2Client, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	cfg := R2Config{
		AccountID:       envValue(lookup, R2AccountIDEnv),
		AccessKeyID:     envValue(lookup, R2AccessKeyIDEnv),
		SecretAccessKey: envValue(lookup, R2SecretAccessKeyEnv),
		Bucket:          envValue(lookup, R2BucketEnv),
	}
	return NewR2(cfg, opts)
}

// NewR2 builds an R2Client from an explicit config. Any required field blank
// returns *NotConfiguredError (never a panic).
func NewR2(cfg R2Config, opts R2Options) (*R2Client, error) {
	if missing := cfg.missing(); len(missing) > 0 {
		return nil, &NotConfiguredError{MissingEnv: missing}
	}

	endpoint := opts.EndpointOverride
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}

	awsCfg := aws.Config{
		Region:      "auto", // Cloudflare R2 (D24/D20): SigV4 requires a region name, R2 ignores its value
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}
	if opts.HTTPClient != nil {
		awsCfg.HTTPClient = opts.HTTPClient
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true // required for R2 and for the httptest fake endpoint in tests
	})

	return &R2Client{
		bucket:  cfg.Bucket,
		client:  client,
		presign: s3.NewPresignClient(client),
	}, nil
}

// Bucket returns the configured bucket name.
func (r *R2Client) Bucket() string { return r.bucket }

// Upload puts body under key in the bucket. size is the exact content length
// (required for S3-compatible PutObject); contentType may be empty.
func (r *R2Client) Upload(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("storage: r2 upload: key is required")
	}
	if size < 0 {
		return fmt.Errorf("storage: r2 upload %s: size must be >= 0, got %d", key, size)
	}
	in := &s3.PutObjectInput{
		Bucket:        aws.String(r.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if _, err := r.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: r2 upload %s: %w", key, err)
	}
	return nil
}

// PresignGET returns a temporary public GET URL for key (Instagram/Facebook
// uploads need these — CONTEXT D20). expiry <= 0 defaults to 1h; it is
// clamped to [1m, 7d], the SigV4 presign limits.
func (r *R2Client) PresignGET(ctx context.Context, key string, expiry time.Duration) (string, time.Duration, error) {
	if strings.TrimSpace(key) == "" {
		return "", 0, errors.New("storage: r2 presign: key is required")
	}
	if expiry <= 0 {
		expiry = defaultPresignExpiry
	}
	if expiry < minPresignExpiry {
		expiry = minPresignExpiry
	}
	if expiry > maxPresignExpiry {
		expiry = maxPresignExpiry
	}
	out, err := r.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", 0, fmt.Errorf("storage: r2 presign %s: %w", key, err)
	}
	return out.URL, expiry, nil
}

// Delete removes key from the bucket. Deleting an already-absent key is not
// an error (S3-compatible DeleteObject is idempotent), which matters for
// cleanup retries.
func (r *R2Client) Delete(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("storage: r2 delete: key is required")
	}
	if _, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("storage: r2 delete %s: %w", key, err)
	}
	return nil
}

func envValue(lookup func(string) (string, bool), key string) string {
	v, ok := lookup(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}
