package httpapi

import (
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
)

// fetchDestKey names the operation metadata entry listing the fetch
// destinations an operation answers, as []string, in place of
// [defaultFetchDests].
const fetchDestKey = "fetchDest"

// The Sec-Fetch-Dest values inbok's operations answer (Fetch Metadata
// Request Headers, 2.2): what the browser loads the response as.
const (
	// A fetch() call, as the web app's client makes.
	fetchDestEmpty = "empty"
	// A page opened in a tab, e.g. a site sending the browser back.
	fetchDestDocument = "document"
	// A page shown in a frame, e.g. a message body in the message view.
	fetchDestIframe = "iframe"
	// An image, e.g. a message's inline part.
	fetchDestImage = "image"
)

// defaultFetchDests are what an operation answers when its metadata lists
// none: the web app's own calls. An operation tagged [tagRedirects] is a
// page another site sends the browser to, so it answers
// [redirectFetchDests] instead.
var (
	defaultFetchDests  = []string{fetchDestEmpty}
	redirectFetchDests = []string{fetchDestDocument}
)

// withFetchDest refuses a browser request whose Sec-Fetch-Dest the
// operation does not answer, before its access check. A request without
// the header, e.g. from curl, passes.
//
// DESIGN: the message view frames a body on inbok's origin, so an <img>
// in it that names an API address goes with the member's session cookie
// and Sec-Fetch-Site same-origin, which the cross-origin check accepts.
// The sanitizer keeps such an address only as an absolute URL once the
// member loads remote content, or by a bug. A GET must change nothing
// anyway; this keeps a body from reaching an operation it was never
// meant to load, e.g. one added later that forgets that rule (ADR009).
func withFetchDest() func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		dest := ctx.Header("Sec-Fetch-Dest")
		if dest == "" || slices.Contains(fetchDests(ctx.Operation()), dest) {
			next(ctx)
			return
		}
		writeProblem(humagin.Unwrap(ctx), fetchDestProblem())
	}
}

// fetchDests returns the fetch destinations op answers.
func fetchDests(op *huma.Operation) []string {
	if dests, ok := op.Metadata[fetchDestKey].([]string); ok {
		return dests
	}
	if slices.Contains(op.Tags, tagRedirects) {
		return redirectFetchDests
	}
	return defaultFetchDests
}

// fetchDestProblem returns the 403 for a browser request that loads an
// operation as something it does not answer, e.g. as an image.
func fetchDestProblem() *problem {
	return newProblem(http.StatusForbidden, problemPrefix+"fetch-destination-refused",
		"Request kind refused", "This address does not answer this kind of browser request.")
}
