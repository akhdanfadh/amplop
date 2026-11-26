package webapp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// policy is the web app's Content-Security-Policy, with %s for the
// script-src hashes of the shell's inline scripts.
//
// The policy is the browser's second line of defense, deciding what
// markup an attacker gets into a page can load, run, or send. 'self' is
// amplop's own origin: its scheme, host, and port.
const policy = "" +
	// Every load not named below only from amplop: images, fonts, fetch.
	"default-src 'self'; " +
	// Scripts only from amplop's files and the shell's inline scripts; no
	// other inline script, no eval.
	"script-src 'self'%s; " +
	// No <object> or <embed> plugins.
	"object-src 'none'; " +
	// No <base> element, which could send relative URLs to another host.
	"base-uri 'none'; " +
	// Forms submit only to amplop.
	"form-action 'self'; " +
	// No site may frame a page.
	"frame-ancestors 'none'"

// newPolicy returns [policy] allowing each inline script in shell, an HTML
// page, by its hash. Returns an error if shell cannot be read.
func newPolicy(shell []byte) (string, error) {
	var hashes strings.Builder
	tokens := html.NewTokenizer(bytes.NewReader(shell))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			if errors.Is(tokens.Err(), io.EOF) {
				return fmt.Sprintf(policy, hashes.String()), nil
			}
			return "", tokens.Err()
		case html.StartTagToken:
			if !isInlineScript(tokens) {
				continue
			}
			// The tokenizer reads script text as browsers do before hashing
			// it: entities kept, newlines normalized, NUL as U+FFFD. An
			// empty script has no text.
			var text []byte
			if tokens.Next() == html.TextToken {
				text = tokens.Text()
			}
			sum := sha256.Sum256(text)
			hashes.WriteString(" 'sha256-")
			hashes.WriteString(base64.StdEncoding.EncodeToString(sum[:]))
			hashes.WriteString("'")
		}
	}
}

// isInlineScript reports whether the start tag that tokens just read is a
// <script> without a src attribute.
func isInlineScript(tokens *html.Tokenizer) bool {
	name, hasAttr := tokens.TagName()
	if string(name) != "script" {
		return false
	}
	for hasAttr {
		var key []byte
		key, _, hasAttr = tokens.TagAttr()
		if string(key) == "src" {
			return false
		}
	}
	return true
}
