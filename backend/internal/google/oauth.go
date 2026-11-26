package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"

	"amplop/internal/connectedaccount"
)

// errorInvalidGrant is the token endpoint's error for a refresh token it
// no longer accepts: revoked, expired, or dropped (RFC 6749 section 5.2).
const errorInvalidGrant = "invalid_grant"

// errorInvalidToken is the revocation endpoint's error for a token it does
// not know, e.g. one already revoked or expired.
const errorInvalidToken = "invalid_token"

// maxRevokeError bounds how much of a refused revocation's body is read for
// its error code.
const maxRevokeError = 4 << 10

// scopes are what amplop asks for: who the account is, and Gmail without
// permanent deletion.
var scopes = []string{"openid", "email", gmail.GmailModifyScope}

// AuthCodeURL returns Google's consent page address for state, asking for a
// refresh token on every connect and sending an S256 challenge for verifier.
func (c *Client) AuthCodeURL(state, verifier string) string {
	return c.oauth.AuthCodeURL(state,
		// Offline access and consent every time, so every connect, a
		// reconnect included, returns a refresh token.
		oauth2.AccessTypeOffline, oauth2.ApprovalForce,
		oauth2.S256ChallengeOption(verifier))
}

// Exchange trades code and verifier for a grant, read from Google's token
// response and its ID token. It returns an error when Google refuses the
// code, or the ID token is missing or fails a check.
func (c *Client) Exchange(ctx context.Context, code, verifier string) (connectedaccount.Grant, error) {
	token, err := c.oauth.Exchange(c.withHTTP(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return connectedaccount.Grant{}, redactToken(err)
	}
	claims, err := c.parseIDToken(token.Extra("id_token"))
	if err != nil {
		return connectedaccount.Grant{}, err
	}
	// The scopes Google granted, which the member may have narrowed on the
	// consent page. Google always sends them; none counts as no Gmail.
	granted, _ := token.Extra("scope").(string)
	return connectedaccount.Grant{
		RefreshToken:  token.RefreshToken,
		AccessToken:   connectedaccount.NewAccessToken(token.AccessToken),
		Identity:      connectedaccount.Identity{Subject: claims.Subject, Email: claims.Email},
		EmailVerified: bool(claims.EmailVerified),
		GmailGranted:  slices.Contains(strings.Fields(granted), gmail.GmailModifyScope),
	}, nil
}

// Refresh trades refreshToken for an access token, with the replacement
// refresh token Google sends, if any. It returns an error wrapping
// [connectedaccount.ErrRefreshRefused] when Google refuses refreshToken.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (connectedaccount.Renewal, error) {
	// A token with no access token makes the source ask for one.
	token, err := c.oauth.TokenSource(c.withHTTP(ctx), &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		err = redactToken(err)
		if refused, ok := errors.AsType[*tokenRefusal](err); ok && refused.code == errorInvalidGrant {
			return connectedaccount.Renewal{}, fmt.Errorf("%w: %w", connectedaccount.ErrRefreshRefused, err)
		}
		return connectedaccount.Renewal{}, err
	}
	renewal := connectedaccount.Renewal{AccessToken: connectedaccount.NewAccessToken(token.AccessToken), Expiry: token.Expiry}
	// x/oauth2 keeps the old refresh token when Google sends none.
	if token.RefreshToken != refreshToken {
		renewal.RefreshToken = token.RefreshToken
	}
	return renewal, nil
}

// Revoke ends refreshToken's grant at Google: the token and every other
// token of the same Google account issued to amplop's Google project. A token
// Google no longer knows counts as revoked. It returns an error with only the
// HTTP status and OAuth error code when Google refuses or fails.
func (c *Client) Revoke(ctx context.Context, refreshToken string) error {
	// The token goes in the body, as RFC 7009 section 2.1 sends it, so no
	// error that quotes the URL carries it.
	form := url.Values{"token": {refreshToken}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusOK {
		return nil
	}
	var refusal struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, maxRevokeError)).Decode(&refusal)
	// RFC 7009 answers 200 for a token the server does not know; Google
	// answers 400 invalid_token. Either way the token no longer works.
	if response.StatusCode == http.StatusBadRequest && refusal.Error == errorInvalidToken {
		return nil
	}
	return &tokenRefusal{status: response.StatusCode, code: refusal.Error}
}

// withHTTP returns ctx carrying the client x/oauth2 sends requests with.
func (c *Client) withHTTP(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, c.http)
}

// redactToken returns err, from the token endpoint, as only its HTTP status
// and OAuth error code: x/oauth2 puts a nonstandard response's whole body
// in its message, which then reaches the logs.
func redactToken(err error) error {
	var refused *oauth2.RetrieveError
	if !errors.As(err, &refused) {
		return err
	}
	status := 0
	if refused.Response != nil {
		status = refused.Response.StatusCode
	}
	return &tokenRefusal{status: status, code: refused.ErrorCode}
}

// tokenRefusal is a refusal from Google's token or revocation endpoint: its
// HTTP status and OAuth error code, e.g. invalid_grant, and nothing else
// from the body.
type tokenRefusal struct {
	status int
	code   string
}

// Error returns the status and code.
func (e *tokenRefusal) Error() string {
	return fmt.Sprintf("google oauth endpoint: %d %s", e.status, e.code)
}
