package google

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"

	"inbok/internal/connectedaccount"
)

// scopes are what inbok asks for (ADR006): who the account is, and Gmail
// without permanent deletion.
var scopes = []string{"openid", "email", gmail.GmailModifyScope}

// AuthCodeURL returns Google's consent page address for state, asking for a
// refresh token on every connect and sending an S256 challenge for verifier.
func (c *Client) AuthCodeURL(state, verifier string) string {
	return c.oauth.AuthCodeURL(state,
		// Offline access and consent every time, so every connect, a
		// reconnect included, returns a refresh token (ADR006).
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
		AccessToken:   token.AccessToken,
		Identity:      connectedaccount.Identity{Subject: claims.Subject, Email: claims.Email},
		EmailVerified: bool(claims.EmailVerified),
		GmailGranted:  slices.Contains(strings.Fields(granted), gmail.GmailModifyScope),
	}, nil
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

// tokenRefusal is a refusal from Google's token endpoint: its HTTP status
// and OAuth error code, e.g. invalid_grant, and nothing from the body.
type tokenRefusal struct {
	status int
	code   string
}

// Error returns the status and code.
func (e *tokenRefusal) Error() string {
	return fmt.Sprintf("google token endpoint: %d %s", e.status, e.code)
}
