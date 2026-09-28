package secrets

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/oauth2"
)

// Platform describes an OAuth provider Mayank can authorize via `mayank2 auth`.
type Platform struct {
	// Name is the canonical key stored in oauth_tokens.platform.
	Name            string
	Aliases         []string
	ClientIDEnv     string
	ClientSecretEnv string
	Endpoint        oauth2.Endpoint
	Scopes          []string
	// UsePKCE enables S256 PKCE (Google, X, Pinterest, LinkedIn). Meta uses a
	// confidential client secret without PKCE.
	UsePKCE bool
}

var platforms = []Platform{
	{
		Name:            "youtube",
		Aliases:         []string{"google"},
		ClientIDEnv:     "GOOGLE_CLIENT_ID",
		ClientSecretEnv: "GOOGLE_CLIENT_SECRET",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
		Scopes: []string{
			"https://www.googleapis.com/auth/youtube.upload",
			"https://www.googleapis.com/auth/youtube.readonly",
			"https://www.googleapis.com/auth/yt-analytics.readonly",
		},
		UsePKCE: true,
	},
	{
		Name:            "meta",
		Aliases:         []string{"facebook", "instagram"},
		ClientIDEnv:     "META_APP_ID",
		ClientSecretEnv: "META_APP_SECRET",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://www.facebook.com/v21.0/dialog/oauth",
			TokenURL: "https://graph.facebook.com/v21.0/oauth/access_token",
		},
		Scopes: []string{
			"pages_show_list",
			"pages_read_engagement",
			"pages_manage_posts",
			"instagram_basic",
			"instagram_content_publish",
		},
		UsePKCE: false,
	},
	{
		Name:            "x",
		Aliases:         []string{"twitter"},
		ClientIDEnv:     "X_CLIENT_ID",
		ClientSecretEnv: "X_CLIENT_SECRET",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://twitter.com/i/oauth2/authorize",
			TokenURL: "https://api.twitter.com/2/oauth2/token",
		},
		Scopes:  []string{"tweet.read", "tweet.write", "users.read", "offline.access"},
		UsePKCE: true,
	},
	{
		Name:            "pinterest",
		Aliases:         nil,
		ClientIDEnv:     "PINTEREST_APP_ID",
		ClientSecretEnv: "PINTEREST_APP_SECRET",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://www.pinterest.com/oauth/",
			TokenURL: "https://api.pinterest.com/v5/oauth/token",
		},
		Scopes:  []string{"boards:read", "pins:read", "pins:write"},
		UsePKCE: true,
	},
	{
		Name:            "linkedin",
		Aliases:         nil,
		ClientIDEnv:     "LINKEDIN_CLIENT_ID",
		ClientSecretEnv: "LINKEDIN_CLIENT_SECRET",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://www.linkedin.com/oauth/v2/authorization",
			TokenURL: "https://www.linkedin.com/oauth/v2/accessToken",
		},
		Scopes:  []string{"openid", "profile", "w_member_social"},
		UsePKCE: true,
	},
}

// LookupPlatform resolves a platform name or alias (case-insensitive).
func LookupPlatform(name string) (Platform, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return Platform{}, fmt.Errorf("secrets: platform name is empty")
	}
	for _, p := range platforms {
		if p.Name == key {
			return p, nil
		}
		for _, a := range p.Aliases {
			if a == key {
				return p, nil
			}
		}
	}
	return Platform{}, fmt.Errorf("secrets: unknown platform %q (want youtube|meta|x|pinterest|linkedin)", name)
}

// KnownPlatforms returns canonical platform names for help text.
func KnownPlatforms() []string {
	out := make([]string, 0, len(platforms))
	for _, p := range platforms {
		out = append(out, p.Name)
	}
	return out
}

// OAuthConfig builds an oauth2.Config from the environment for this platform.
// redirectURL must be the loopback URL used for the auth code flow.
func (p Platform) OAuthConfig(redirectURL string) (*oauth2.Config, error) {
	id := strings.TrimSpace(os.Getenv(p.ClientIDEnv))
	secret := strings.TrimSpace(os.Getenv(p.ClientSecretEnv))
	if id == "" {
		return nil, fmt.Errorf("secrets: %s is not set (needed for %s)", p.ClientIDEnv, p.Name)
	}
	if secret == "" {
		return nil, fmt.Errorf("secrets: %s is not set (needed for %s)", p.ClientSecretEnv, p.Name)
	}
	if redirectURL == "" {
		return nil, fmt.Errorf("secrets: redirect URL is required for %s", p.Name)
	}
	return &oauth2.Config{
		ClientID:     id,
		ClientSecret: secret,
		Endpoint:     p.Endpoint,
		RedirectURL:  redirectURL,
		Scopes:       append([]string(nil), p.Scopes...),
	}, nil
}
