package publish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/secrets"
)

// fakeXAPI records CreateTweet calls so tests can assert reply-chaining and
// call counts without any real network.
type fakeXPersonalAPI struct {
	calls  []struct{ text, replyTo string }
	nextID int
	fail   bool
}

func (f *fakeXPersonalAPI) CreateTweet(ctx context.Context, tok *oauth2.Token, text, replyToID string) (string, error) {
	f.calls = append(f.calls, struct{ text, replyTo string }{text, replyToID})
	if f.fail {
		return "", errors.New("simulated api failure")
	}
	f.nextID++
	return "tw-" + itoa(f.nextID), nil
}

func itoa(n int) string {
	// tiny local itoa to avoid pulling in strconv just for test ids
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// fakeTokenStore is a map-backed TokenStore keyed by (platform, account),
// mirroring internal/secrets.Store's real (platform, account) primary key.
type fakeTokenStore struct {
	tokens map[[2]string]*oauth2.Token
	gets   [][2]string // records every (platform, account) pair requested
}

func newFakeTokenStore() *fakeTokenStore {
	return &fakeTokenStore{tokens: map[[2]string]*oauth2.Token{}}
}

func (f *fakeTokenStore) put(platform, account, accessToken string) {
	f.tokens[[2]string{platform, account}] = &oauth2.Token{AccessToken: accessToken}
}

func (f *fakeTokenStore) Get(ctx context.Context, platform, account string) (*oauth2.Token, error) {
	f.gets = append(f.gets, [2]string{platform, account})
	tok, ok := f.tokens[[2]string{platform, account}]
	if !ok {
		return nil, errors.New("no token")
	}
	return tok, nil
}

func threadJSON(t *testing.T, tweets ...string) string {
	t.Helper()
	raw, err := json.Marshal(tweets)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// personalTokens returns a fakeTokenStore pre-seeded with a token under the
// real convention: platform="x" (SecretsPlatform), account=DefaultAccountRef
// ("x-personal").
func personalTokens(accessToken string) *fakeTokenStore {
	ts := newFakeTokenStore()
	ts.put(SecretsPlatform, DefaultAccountRef, accessToken)
	return ts
}

func TestXPersonal_PublishHappyPath(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'scheduled', 'c1:x_personal:x-personal')`)

	tokens := personalTokens("personal-access-token")
	api := &fakeXPersonalAPI{}
	x := &XPersonal{
		DB: sqlDB, Tokens: tokens, API: api,
		Now: func() time.Time { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) },
	}

	desc := threadJSON(t, "hook tweet one", "second tweet in the thread", "third and last, link in bio")
	res, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "x-personal", IdempotencyKey: "c1:x_personal:x-personal",
		Description: desc,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "tw-1" || !strings.Contains(res.URL, "tw-1") {
		t.Fatalf("result=%+v", res)
	}
	if len(api.calls) != 3 {
		t.Fatalf("want 3 CreateTweet calls, got %d", len(api.calls))
	}
	if api.calls[0].replyTo != "" {
		t.Fatalf("first tweet must be root (no reply-to), got %q", api.calls[0].replyTo)
	}
	if api.calls[1].replyTo != "tw-1" || api.calls[2].replyTo != "tw-2" {
		t.Fatalf("reply chain wrong: %+v", api.calls)
	}

	var status string
	_ = sqlDB.QueryRow(`SELECT status FROM publications WHERE id='pub1'`).Scan(&status)
	if status != "published" {
		t.Fatalf("status=%q want published", status)
	}
	var used int
	_ = sqlDB.QueryRow(`SELECT used FROM quotas WHERE provider='x:x-personal'`).Scan(&used)
	if used != 1 {
		t.Fatalf("cap used=%d want 1 (one thread = one post)", used)
	}
}

func TestXPersonal_PublishDefaultsAccountWhenUnset(t *testing.T) {
	// PublishRequest.Account is optional: an unset Account must resolve to
	// DefaultAccountRef ("x-personal"), not an empty/shared string.
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'scheduled', 'key1')`)

	tokens := personalTokens("personal-access-token")
	x := &XPersonal{DB: sqlDB, Tokens: tokens, API: &fakeXPersonalAPI{}}
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", IdempotencyKey: "key1",
		Description: threadJSON(t, "hook"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(tokens.gets) != 1 || tokens.gets[0][1] != DefaultAccountRef {
		t.Fatalf("want token lookup account=%q, got %+v", DefaultAccountRef, tokens.gets)
	}
}

func TestXPersonal_RefusesNonPersonalAccount(t *testing.T) {
	// Defense in depth: even if a caller mistakenly supplies a
	// business-looking account ref, XPersonal must refuse before touching
	// approval/cap/API at all.
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	api := &fakeXPersonalAPI{}
	x := &XPersonal{DB: sqlDB, Tokens: newFakeTokenStore(), API: api}
	_, err := x.Publish(context.Background(), PublishRequest{
		ContentID: "c1", Account: "x-business", IdempotencyKey: "key1",
		Description: threadJSON(t, "hook"),
	})
	if err == nil || !strings.Contains(err.Error(), "non-personal") {
		t.Fatalf("want refusal of a non-personal account ref, got %v", err)
	}
	if len(api.calls) != 0 {
		t.Fatal("must never call the API for a non-personal account ref")
	}
}

func TestXPersonal_Idempotent(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, external_id, url, idempotency_key, published_at)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'published', 'tw-old', 'https://x.com/i/status/tw-old', 'key1', ?)`,
		time.Now().UTC().Format(time.RFC3339Nano))

	api := &fakeXPersonalAPI{}
	x := &XPersonal{DB: sqlDB, Tokens: newFakeTokenStore(), API: api}
	res, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "x-personal", IdempotencyKey: "key1",
		Description: threadJSON(t, "irrelevant, should never be sent"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "tw-old" || len(api.calls) != 0 {
		t.Fatalf("idempotent replay must be a no-op: res=%+v calls=%d", res, len(api.calls))
	}
}

func TestXPersonal_PartialThreadRecovery(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	// Simulate a crash after tweet 1 of 3 posted: status still 'publishing',
	// external_id holds the progress array, and the cap was already spent
	// on the first attempt.
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, external_id, idempotency_key)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'publishing', '["tw-1"]', 'key1')`)
	mustExec(t, sqlDB, `
INSERT INTO quotas (provider, window_start, used, "limit") VALUES ('x:x-personal', ?, 1, 3)`,
		time.Now().UTC().Truncate(24*time.Hour).Format(time.RFC3339Nano))

	tokens := personalTokens("personal-access-token")
	api := &fakeXPersonalAPI{nextID: 1} // next call must produce tw-2, tw-3
	x := &XPersonal{
		DB: sqlDB, Tokens: tokens, API: api,
		Now: func() time.Time { return time.Now().UTC() },
	}
	res, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "x-personal", IdempotencyKey: "key1",
		Description: threadJSON(t, "tweet one (already posted)", "tweet two", "tweet three"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(api.calls) != 2 {
		t.Fatalf("want exactly 2 new posts (resume from tweet 2), got %d: %+v", len(api.calls), api.calls)
	}
	if api.calls[0].text != "tweet two" || api.calls[0].replyTo != "tw-1" {
		t.Fatalf("resume did not chain off the stored tweet: %+v", api.calls[0])
	}
	if res.ExternalID != "tw-1" {
		t.Fatalf("root tweet id must stay the first-ever posted id, got %q", res.ExternalID)
	}
	var used int
	_ = sqlDB.QueryRow(`SELECT used FROM quotas WHERE provider='x:x-personal'`).Scan(&used)
	if used != 1 {
		t.Fatalf("cap must not be double-charged on resume: used=%d want 1", used)
	}
}

func TestXPersonal_RequiresApproval(t *testing.T) {
	sqlDB := openPubDB(t)
	mustExec(t, sqlDB, `
INSERT INTO channels (id, platform, language, niche, status) VALUES ('blog', 'blog', 'en', 'blog', 'active')`)
	mustExec(t, sqlDB, `
INSERT INTO content_items (id, channel_id, kind, language, stage, created_at)
VALUES ('c1', 'blog', 'blog', 'en', 'draft', ?)`, time.Now().UTC().Format(time.RFC3339Nano))

	x := &XPersonal{DB: sqlDB, Tokens: newFakeTokenStore(), API: &fakeXPersonalAPI{}}
	_, err := x.Publish(context.Background(), PublishRequest{
		ContentID: "c1", Account: "x-personal", IdempotencyKey: "k1",
		Description: threadJSON(t, "hook"),
	})
	if err == nil || !strings.Contains(err.Error(), "no approved approval") {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestXPersonal_OverLengthTweetRejected(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'scheduled', 'key1')`)

	over := strings.Repeat("a", 281)
	api := &fakeXPersonalAPI{}
	x := &XPersonal{DB: sqlDB, Tokens: newFakeTokenStore(), API: api}
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "x-personal", IdempotencyKey: "key1",
		Description: threadJSON(t, over),
	})
	if err == nil || !strings.Contains(err.Error(), "281") {
		t.Fatalf("want over-length rejection naming the length, got %v", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("must reject before ever calling the API, got %d calls", len(api.calls))
	}
}

func TestXPersonal_CapExceeded(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'scheduled', 'key1')`)
	mustExec(t, sqlDB, `
INSERT INTO quotas (provider, window_start, used, "limit") VALUES ('x:x-personal', ?, 3, 3)`,
		time.Now().UTC().Truncate(24*time.Hour).Format(time.RFC3339Nano))

	api := &fakeXPersonalAPI{}
	x := &XPersonal{
		DB: sqlDB, Tokens: newFakeTokenStore(), API: api,
		Now: func() time.Time { return time.Now().UTC() },
	}
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "x-personal", IdempotencyKey: "key1",
		Description: threadJSON(t, "hook"),
	})
	if err == nil || !strings.Contains(err.Error(), "daily cap exceeded") {
		t.Fatalf("want daily cap exceeded (fail closed), got %v", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("must fail closed before ever calling the API, got %d calls", len(api.calls))
	}
	var status string
	_ = sqlDB.QueryRow(`SELECT status FROM publications WHERE id='pub1'`).Scan(&status)
	if status != "failed" {
		t.Fatalf("status=%q want failed", status)
	}
}

// TestXPersonal_CapIsolatedFromBusinessCap proves the ≤3/day personal cap
// and the ≤5/day business cap (COMPLIANCE §4) never share a bucket, even
// though both now live under the same "x:" quotas provider prefix
// (internal/publish/x.go's reserveCap convention, mirrored here as
// quotaProvider). A business account maxed out at its own cap must not
// block the personal account, and vice versa.
func TestXPersonal_CapIsolatedFromBusinessCap(t *testing.T) {
	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'scheduled', 'key1')`)
	// The business account ("x-business") is already at its cap. This must
	// have zero effect on the personal account's own bucket.
	mustExec(t, sqlDB, `
INSERT INTO quotas (provider, window_start, used, "limit") VALUES ('x:x-business', ?, 5, 5)`,
		time.Now().UTC().Truncate(24*time.Hour).Format(time.RFC3339Nano))

	tokens := personalTokens("personal-access-token")
	x := &XPersonal{DB: sqlDB, Tokens: tokens, API: &fakeXPersonalAPI{}}
	_, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "x-personal", IdempotencyKey: "key1",
		Description: threadJSON(t, "hook"),
	})
	if err != nil {
		t.Fatalf("personal publish must succeed despite the business account being capped out: %v", err)
	}
}

// TestXPersonal_CredentialIsolation is the ticket's hard requirement: prove
// the personal publisher can never resolve, or fall back to, the business
// account's token or a shared default.
//
// M2-303 (internal/publish/x.go) was not built when this design started
// (verified at the time via `git log m2/M2-303`: no commits past the shared
// phase base) but landed mid-session. Its real, merged convention — read
// directly from internal/publish/x.go's header comment and locked in by its
// own TestX_AccountRefNamingConvention — is: internal/secrets keys tokens by
// (platform="x", account) for BOTH accounts (there is only one registered
// "x" platform in internal/secrets/platform.go), separated only by
// account_ref, defended by x.go's isPersonalXAccount(account) guard which
// refuses any ref containing "personal". SecretsPlatform/DefaultAccountRef
// below match that real convention exactly (not a placeholder guess).
func TestXPersonal_CredentialIsolation(t *testing.T) {
	const businessAccount = "x-business" // internal/publish/x.go's own test convention

	if SecretsPlatform != "x" {
		t.Fatalf("SecretsPlatform=%q must match internal/publish/x.go's real platform key %q", SecretsPlatform, "x")
	}
	if !looksPersonalXAccount(DefaultAccountRef) {
		t.Fatalf("DefaultAccountRef %q must satisfy x.go's isPersonalXAccount guard (contain \"personal\")", DefaultAccountRef)
	}
	if looksPersonalXAccount(businessAccount) {
		t.Fatalf("businessAccount %q must NOT look personal (sanity check on the test fixture itself)", businessAccount)
	}
	if DefaultAccountRef == businessAccount {
		t.Fatalf("x_personal default account_ref must not equal a business account_ref")
	}

	// Two tokens stored under the same platform ("x") but disjoint accounts
	// must resolve independently: fetching one must never return the
	// other's token.
	tokens := newFakeTokenStore()
	tokens.put(SecretsPlatform, businessAccount, "BUSINESS-TOKEN-SHOULD-NEVER-BE-USED-HERE")
	tokens.put(SecretsPlatform, DefaultAccountRef, "personal-token-ok")

	x := &XPersonal{Tokens: tokens}
	tok, err := x.token(context.Background(), DefaultAccountRef)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if tok.AccessToken != "personal-token-ok" {
		t.Fatalf("resolved token=%q, want the personal token only", tok.AccessToken)
	}
	if tok.AccessToken == "BUSINESS-TOKEN-SHOULD-NEVER-BE-USED-HERE" {
		t.Fatal("x_personal resolved the business account's token")
	}

	// The exact (platform, account) pair requested must be the personal
	// one — never the business account_ref.
	if len(tokens.gets) != 1 {
		t.Fatalf("want exactly 1 token lookup, got %d", len(tokens.gets))
	}
	got := tokens.gets[0]
	if got[0] != SecretsPlatform || got[1] != DefaultAccountRef {
		t.Fatalf("token lookup used (%q, %q), want (%q, %q)", got[0], got[1], SecretsPlatform, DefaultAccountRef)
	}
	if got[1] == businessAccount {
		t.Fatalf("token lookup touched the business account_ref: %+v", got)
	}

	// Publish itself must refuse to even attempt a business-looking
	// account_ref (belt-and-suspenders — proven end-to-end in
	// TestXPersonal_RefusesNonPersonalAccount).
}

// TestXPersonal_CredentialIsolation_RealSecretsStore repeats the isolation
// proof against the real internal/secrets.Store (DPAPI-backed on Windows),
// not just an in-memory fake, so the guarantee holds against the actual
// storage layer both publishers use in production.
func TestXPersonal_CredentialIsolation_RealSecretsStore(t *testing.T) {
	sqlDB := openPubDB(t)
	store := secrets.NewStore(sqlDB)
	ctx := context.Background()

	const businessAccount = "x-business"
	if err := store.Put(ctx, SecretsPlatform, businessAccount, &oauth2.Token{AccessToken: "business-real-token"}); err != nil {
		t.Fatalf("seed business token: %v", err)
	}
	if err := store.Put(ctx, SecretsPlatform, DefaultAccountRef, &oauth2.Token{AccessToken: "personal-real-token"}); err != nil {
		t.Fatalf("seed personal token: %v", err)
	}

	x := &XPersonal{Tokens: store}
	tok, err := x.token(ctx, DefaultAccountRef)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if tok.AccessToken != "personal-real-token" {
		t.Fatalf("XPersonal resolved %q via the real secrets.Store, want the personal token", tok.AccessToken)
	}

	businessTok, err := store.Get(ctx, SecretsPlatform, businessAccount)
	if err != nil {
		t.Fatalf("get business token: %v", err)
	}
	if businessTok.AccessToken == tok.AccessToken {
		t.Fatal("business and personal tokens must not be equal/shared in the real store")
	}
}

// TestLooksPersonalXAccount locks in the same convention as
// internal/publish/x.go's TestX_AccountRefNamingConvention, from the
// personal-publisher side.
func TestLooksPersonalXAccount(t *testing.T) {
	cases := []struct {
		account string
		want    bool
	}{
		{"x-business", false},
		{"x-business-money-en", false},
		{"xb", false},
		{"x_business", false},
		{"personal", true},
		{"x-personal", true},
		{"x_personal", true},
		{"X-PERSONAL", true},
		{"mayank-personal", true},
		{"x-business-personal-blend", true},
	}
	for _, tc := range cases {
		t.Run(tc.account, func(t *testing.T) {
			if got := looksPersonalXAccount(tc.account); got != tc.want {
				t.Fatalf("looksPersonalXAccount(%q) = %v, want %v", tc.account, got, tc.want)
			}
		})
	}
}

// TestXPersonal_HTTPAPI_HappyPath exercises HTTPXPersonalAPI (the real
// X API v2 HTTP client, not the interface fake used by the other tests)
// against a fake X API server via httptest, end to end through
// XPersonal.Publish, per the ticket's "happy-path publish against a fake X
// API server (httptest)" acceptance criterion.
func TestXPersonal_HTTPAPI_HappyPath(t *testing.T) {
	var gotAuth []string
	var gotBodies []map[string]any
	nextID := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBodies = append(gotBodies, body)
		nextID++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"id": "tw-" + itoa(nextID), "text": body["text"]},
		})
	}))
	defer srv.Close()

	sqlDB := openPubDB(t)
	seedApproved(t, sqlDB, "c1", "blog")
	mustExec(t, sqlDB, `
INSERT INTO publications (id, content_id, platform, account, status, idempotency_key)
VALUES ('pub1', 'c1', 'x_personal', 'x-personal', 'scheduled', 'key1')`)

	tokens := personalTokens("http-test-access-token")
	x := &XPersonal{
		DB: sqlDB, Tokens: tokens,
		API: &HTTPXPersonalAPI{BaseURL: srv.URL},
	}

	res, err := x.Publish(context.Background(), PublishRequest{
		PublicationID: "pub1", ContentID: "c1", Account: "x-personal", IdempotencyKey: "key1",
		Description: threadJSON(t, "hook via real http client", "reply tweet via real http client"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.ExternalID != "tw-1" {
		t.Fatalf("result=%+v", res)
	}
	if len(gotBodies) != 2 {
		t.Fatalf("want 2 HTTP requests, got %d", len(gotBodies))
	}
	if gotAuth[0] != "Bearer http-test-access-token" {
		t.Fatalf("authorization header=%q", gotAuth[0])
	}
	if _, hasReply := gotBodies[0]["reply"]; hasReply {
		t.Fatal("first tweet must not carry a reply field")
	}
	reply, _ := gotBodies[1]["reply"].(map[string]any)
	if reply == nil || reply["in_reply_to_tweet_id"] != "tw-1" {
		t.Fatalf("second tweet must reply to the first: body=%+v", gotBodies[1])
	}
}

// TestXPersonal_HTTPAPI_ErrorRedactsBody proves an X API error body never
// leaks a bearer-shaped token substring into the wrapped error (redact is
// the same helper youtube.go already uses in this package).
func TestXPersonal_HTTPAPI_ErrorRedactsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"title":"Unauthorized","detail":"bearer abc.def.SECRET was rejected"}`))
	}))
	defer srv.Close()

	api := &HTTPXPersonalAPI{BaseURL: srv.URL}
	_, err := api.CreateTweet(context.Background(), &oauth2.Token{AccessToken: "tok"}, "hello", "")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error leaked the token substring: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("want redacted marker in error: %v", err)
	}
}
