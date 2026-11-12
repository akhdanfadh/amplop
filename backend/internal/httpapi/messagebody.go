package httpapi

import (
	"bytes"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"inbok/internal/messagecontent"
)

// bodyContentType is a message body's type: [messagecontent.Sanitize]
// writes UTF-8 and names no charset in the document.
const bodyContentType = "text/html; charset=utf-8"

// The Content-Security-Policy of a message body, the second layer behind
// [messagecontent.Sanitize]: scripts, forms, frames, fonts, and remote
// images the sanitizer missed are still refused.
const (
	// Repeats the message view's iframe sandbox, so a body opened on its
	// own runs no script either. Without allow-scripts, nothing in the
	// body runs; allow-same-origin keeps its origin, so its inline images
	// load with the session cookie and the view can measure its height;
	// links open in a new tab, outside the sandbox.
	bodySandbox = "sandbox allow-same-origin allow-popups allow-popups-to-escape-sandbox"
	// Nothing loads but what the policy names, and only inbok may frame
	// the body. The body's own CSS, in <style> and style attributes,
	// applies; @import is a load and stays refused.
	bodyPolicy = bodySandbox + "; default-src 'none'; style-src 'unsafe-inline'; " +
		"form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
	// Inline images are part URLs on inbok's own origin. 'self' also
	// matches other API paths; [withFetchDest] refuses those as images.
	bodyImagesBlocked = "; img-src 'self'"
	// Remote images are the http and https URLs the sanitizer kept.
	bodyImagesAllowed = "; img-src 'self' http: https:"
)

// messageBody returns the response that sends html, a stored HTML part, as the
// sanitized document the message view shows in its iframe, under policy. It
// returns the error that stopped reading html. The operation that returns it
// lists [fetchDestIframe] under [fetchDestKey].
//
// DESIGN: the body has a URL of its own rather than the iframe's srcdoc: a
// srcdoc document takes the web app's policy, which refuses the message's CSS,
// while a response brings its own (ADR009). The whole copy is sanitized before
// the answer starts, so a store that fails midway gets an error answer, which
// the view shows as a failure, rather than a message cut short that reads as
// complete; the copy is about the size of the stored HTML.
func messageBody(html io.Reader, policy messagecontent.Policy) (*huma.StreamResponse, error) {
	var body bytes.Buffer
	if _, err := messagecontent.Sanitize(&body, html, policy); err != nil {
		return nil, err
	}
	images := bodyImagesBlocked
	if policy.Remote == messagecontent.RemoteAllowed {
		images = bodyImagesAllowed
	}
	return &huma.StreamResponse{Body: func(ctx huma.Context) {
		ctx.SetHeader("Content-Type", bodyContentType)
		ctx.SetHeader("Content-Security-Policy", bodyPolicy+images)
		// Replaces the DENY of [withSecurityHeaders]: the web app frames the
		// body. frame-ancestors says the same to browsers with CSP Level 2.
		ctx.SetHeader("X-Frame-Options", "SAMEORIGIN")
		// The member's mail, which no cache may keep.
		ctx.SetHeader("Cache-Control", "no-store")
		ctx.SetStatus(http.StatusOK)
		// A failed write means the browser left; there is no one to tell.
		_, _ = ctx.BodyWriter().Write(body.Bytes())
	}}, nil
}
