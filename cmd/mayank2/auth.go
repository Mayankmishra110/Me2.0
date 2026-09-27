package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"mayank2/internal/config"
	"mayank2/internal/db"
	"mayank2/internal/secrets"
)

const authTimeout = 5 * time.Minute

func cmdAuth(ctx context.Context, args []string) int {
	configPath, envPath, dbPath, platformArg, account, err := parseAuthFlags(args)
	if err == errHelp {
		fmt.Fprint(os.Stderr, authUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		return 2
	}

	plat, err := secrets.LookupPlatform(platformArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		return 2
	}

	if err := config.LoadEnvFile(envPath); err != nil {
		fmt.Fprintf(os.Stderr, "auth: load env: %v\n", err)
		return 1
	}

	if dbPath == "" {
		cfgPath := configPath
		if !fileExists(cfgPath) {
			example := filepath.Join(filepath.Dir(cfgPath), "config.example.yaml")
			if fileExists(example) {
				cfgPath = example
			}
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "auth: load config: %v\n", err)
			return 1
		}
		dbPath = db.DefaultPath(cfg.DataDir)
	}

	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: open db: %v\n", err)
		return 1
	}
	defer sqlDB.Close()
	if _, err := db.Migrate(ctx, sqlDB); err != nil {
		fmt.Fprintf(os.Stderr, "auth: migrate: %v\n", err)
		return 1
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: listen loopback: %v\n", err)
		return 1
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	oauthCfg, err := plat.OAuthConfig(redirectURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		return 1
	}

	state, err := randomState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth: state: %v\n", err)
		return 1
	}

	var authOpts []oauth2.AuthCodeOption
	var verifier string
	if plat.UsePKCE {
		verifier = oauth2.GenerateVerifier()
		authOpts = append(authOpts, oauth2.S256ChallengeOption(verifier))
	}
	// Offline access / refresh tokens where the provider supports it.
	authOpts = append(authOpts, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	authURL := oauthCfg.AuthCodeURL(state, authOpts...)

	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		send := func(r result) {
			select {
			case resCh <- r:
			default:
			}
		}
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			send(result{err: fmt.Errorf("provider error %s: %s", errParam, desc)})
			http.Error(w, "authorization failed; you can close this tab", http.StatusBadRequest)
			return
		}
		if q.Get("state") != state {
			send(result{err: fmt.Errorf("state mismatch")})
			http.Error(w, "state mismatch; you can close this tab", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			send(result{err: fmt.Errorf("missing code")})
			http.Error(w, "missing code; you can close this tab", http.StatusBadRequest)
			return
		}
		send(result{code: code})
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprintf(w, "Mayank 2.0: %s/%s authorized. You can close this tab.", plat.Name, account)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "auth: open this URL to sign in to %s as %q:\n%s\n", plat.Name, account, authURL)
	openBrowser(authURL)

	authCtx, cancel := context.WithTimeout(ctx, authTimeout)
	defer cancel()

	var code string
	select {
	case <-authCtx.Done():
		fmt.Fprintf(os.Stderr, "auth: timed out waiting for callback\n")
		return 1
	case r := <-resCh:
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "auth: %v\n", r.err)
			return 1
		}
		code = r.code
	}

	var exchangeOpts []oauth2.AuthCodeOption
	if plat.UsePKCE {
		exchangeOpts = append(exchangeOpts, oauth2.VerifierOption(verifier))
	}
	tok, err := oauthCfg.Exchange(ctx, code, exchangeOpts...)
	if err != nil {
		// Do not print the raw error — providers sometimes echo secrets in bodies.
		fmt.Fprintf(os.Stderr, "auth: token exchange failed for %s/%s\n", plat.Name, account)
		return 1
	}

	store := secrets.NewStore(sqlDB)
	if err := store.Put(ctx, plat.Name, account, tok); err != nil {
		fmt.Fprintf(os.Stderr, "auth: store: %v\n", err)
		return 1
	}

	fmt.Printf("auth: stored token for %s/%s (encrypted; not printed)\n", plat.Name, account)
	return 0
}

const authUsage = `mayank2 auth — OAuth sign-in for a platform account

Usage:
  mayank2 auth <platform> <account> [flags]

Platforms: youtube (google), meta (facebook|instagram), x (twitter), pinterest, linkedin

Flags:
  -config path   config YAML (default: config/config.yaml)
  -env path      .env file (default: .env)
  -db path       sqlite file (default: <data_dir>/mayank2.db)

The helper listens on 127.0.0.1 (random port), opens the provider consent URL,
exchanges the code, and stores the token encrypted with DPAPI. Tokens are never printed.
`

func parseAuthFlags(args []string) (configPath, envPath, dbPath, platform, account string, err error) {
	configPath, envPath, _ = defaultPaths()
	var rest []string
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 >= len(args) {
				return "", "", "", "", "", fmt.Errorf("%s needs a value", a)
			}
			configPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-config="):
			configPath = strings.TrimPrefix(a, "-config=")
			i++
		case a == "-env" || a == "--env":
			if i+1 >= len(args) {
				return "", "", "", "", "", fmt.Errorf("%s needs a value", a)
			}
			envPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-env="):
			envPath = strings.TrimPrefix(a, "-env=")
			i++
		case a == "-db" || a == "--db":
			if i+1 >= len(args) {
				return "", "", "", "", "", fmt.Errorf("%s needs a value", a)
			}
			dbPath = args[i+1]
			i += 2
		case strings.HasPrefix(a, "-db="):
			dbPath = strings.TrimPrefix(a, "-db=")
			i++
		case a == "-h" || a == "--help":
			return "", "", "", "", "", errHelp
		case strings.HasPrefix(a, "-"):
			return "", "", "", "", "", fmt.Errorf("unknown flag %s", a)
		default:
			rest = append(rest, a)
			i++
		}
	}
	if len(rest) != 2 {
		return "", "", "", "", "", fmt.Errorf("want: auth <platform> <account> (got %v)", rest)
	}
	return configPath, envPath, dbPath, rest[0], rest[1], nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(url string) {
	// Best-effort only; never fail the auth flow if the OS open fails.
	// rundll32 avoids cmd's start.exe mangling of & in OAuth query strings.
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	_ = cmd.Start()
}
