package google

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Errors for an ID token that fails a check. None includes a claim's value.
var (
	errNoIDToken       = errors.New("token response has no id_token")
	errIDTokenFormat   = errors.New("id_token is not a JWT")
	errIDTokenIssuer   = errors.New("id_token names another issuer")
	errIDTokenAudience = errors.New("id_token is for another client")
	errIDTokenExpired  = errors.New("id_token has expired")
	errIDTokenIdentity = errors.New("id_token has no subject or email")
)

// idClaims are the ID token claims inbok reads.
type idClaims struct {
	Issuer        string       `json:"iss"`
	Audience      audience     `json:"aud"`
	Subject       string       `json:"sub"`
	Email         string       `json:"email"`
	EmailVerified flexibleBool `json:"email_verified"`
	Expiry        int64        `json:"exp"`
}

// parseIDToken returns the claims of value, the token response's id_token,
// after checking its issuer, audience, expiry, subject, and email.
//
// DESIGN: the signature is not checked. The token came straight from
// Google's token endpoint over HTTPS, which Google documents as enough
// (ADR006).
func (c *Client) parseIDToken(value any) (idClaims, error) {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return idClaims{}, errNoIDToken
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return idClaims{}, errIDTokenFormat
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idClaims{}, errIDTokenFormat
	}
	var claims idClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return idClaims{}, errIDTokenFormat
	}

	// The issuer must be Google's, or the emulator's when one is configured
	// (OpenID Connect Core 3.1.3.7).
	issuer := claims.Issuer
	if c.normalize {
		issuer = normalizeIssuer(issuer)
	}
	if !slices.Contains(c.issuers, issuer) {
		return idClaims{}, errIDTokenIssuer
	}
	// OpenID Connect allows other audiences beside the client; inbok's choice
	// refuses them, since they would make the token someone else's too.
	if !slices.Equal(claims.Audience, []string{c.oauth.ClientID}) {
		return idClaims{}, errIDTokenAudience
	}
	// The current time must be before exp (OpenID Connect Core 3.1.3.7).
	if !c.now().Before(time.Unix(claims.Expiry, 0)) {
		return idClaims{}, errIDTokenExpired
	}
	// inbok saves an account under its sub and shows it by its email.
	if claims.Subject == "" || claims.Email == "" {
		return idClaims{}, errIDTokenIdentity
	}
	return claims, nil
}

// audience reads aud, which JWT allows as one string or a list.
type audience []string

// UnmarshalJSON reads data, a string or a list of strings, into a.
func (a *audience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// flexibleBool reads email_verified.
//
// NOTE: Google has sent it both as a JSON boolean and as the text "true",
// and its own guide shows the text.
type flexibleBool bool

// UnmarshalJSON reads data, a boolean or "true" or "false", into b.
func (b *flexibleBool) UnmarshalJSON(data []byte) error {
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		*b = flexibleBool(value)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	*b = flexibleBool(text == "true")
	return nil
}

// normalizeIssuer returns issuer with an http or https URL's scheme and host
// lowercased and one trailing slash dropped, as config spells the base URL;
// any other value, one with a username included, is returned as is.
func normalizeIssuer(issuer string) string {
	parsed, err := url.Parse(issuer)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return issuer
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return issuer
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}
