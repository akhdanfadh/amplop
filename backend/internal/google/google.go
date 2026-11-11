// Package google is the technology package for Google: the OAuth client
// members connect Gmail with, the checks on the ID token it returns, the
// Gmail API calls connectedaccount needs, and the reading of a Gmail
// message's parts for messagecontent. OAuth and Gmail types stay here;
// features see their own.
//
// google.go builds the client for Google or the emulator; oauth.go asks for
// consent, trades the code and refresh tokens, and revokes them; idtoken.go
// checks the ID token; gmail.go calls Gmail; transport.go is the HTTP client
// that carries codes and tokens; receivedpart.go turns a Gmail message's
// parts into messagecontent's input.
package google

import (
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
)

// Google's own addresses. The consent page is the v2 one Google's guide
// and discovery document name; [endpoints.Google] still has the older path.
const (
	googleAuthURL   = "https://accounts.google.com/o/oauth2/v2/auth"
	googleRevokeURL = "https://oauth2.googleapis.com/revoke"
	gmailURL        = "https://gmail.googleapis.com/"
)

// googleIssuers are the issuers Google's own ID tokens name.
var googleIssuers = []string{"https://accounts.google.com", "accounts.google.com"}

// Settings configure the client.
type Settings struct {
	ClientID     string
	ClientSecret string
	// RedirectURL is inbok's callback address.
	RedirectURL string
	// BaseURL is one server on this machine that answers for Google, e.g.
	// the emulator, with the emulator's paths. Nil uses Google's hosts.
	BaseURL *url.URL
}

// Client talks to Google for connectedaccount.
type Client struct {
	oauth     *oauth2.Config
	http      *http.Client
	issuers   []string
	revokeURL string
	gmailURL  string
	// normalize is whether an ID token's issuer is compared as config spells
	// a base URL; Google's own issuers are compared exactly.
	normalize bool
	now       func() time.Time
}

// New returns a client for settings, given a clock for the ID token's
// expiry.
func New(settings Settings, now func() time.Time) *Client {
	endpoint := oauth2.Endpoint{AuthURL: googleAuthURL, TokenURL: endpoints.Google.TokenURL}
	issuers := googleIssuers
	revoke := googleRevokeURL
	gmail := gmailURL
	reach := reachAny
	if settings.BaseURL != nil {
		// The emulator's paths (ADR006, Testing); its ID tokens name its own
		// origin as issuer.
		endpoint = oauth2.Endpoint{
			AuthURL:  settings.BaseURL.JoinPath("/o/oauth2/v2/auth").String(),
			TokenURL: settings.BaseURL.JoinPath("/oauth2/token").String(),
		}
		issuers = []string{origin(settings.BaseURL)}
		revoke = settings.BaseURL.JoinPath("/oauth2/revoke").String()
		gmail = origin(settings.BaseURL) + "/"
		reach = reachLoopback
	}
	// Send the secret in the form, as Google's guide does, rather than
	// letting x/oauth2 probe which style the server takes.
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return &Client{
		oauth: &oauth2.Config{
			ClientID:     settings.ClientID,
			ClientSecret: settings.ClientSecret,
			Endpoint:     endpoint,
			RedirectURL:  settings.RedirectURL,
			Scopes:       scopes,
		},
		http:      httpClient(reach, requestTimeout),
		issuers:   issuers,
		revokeURL: revoke,
		gmailURL:  gmail,
		normalize: settings.BaseURL != nil,
		now:       now,
	}
}

// origin returns base's scheme and host, as config already normalized them.
func origin(base *url.URL) string {
	return base.Scheme + "://" + base.Host
}
