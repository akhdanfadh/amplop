package messagecontent

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/microcosm-cc/bluemonday"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

// partURL is the [Policy.PartURL] of the tests.
func partURL(partID string) string {
	return "/parts/" + partID
}

// sanitize returns [Sanitize]'s copy of input under policy, without the
// doctype every copy starts with, and what it found.
func sanitize(t *testing.T, input string, policy Policy) (string, Sanitized) {
	t.Helper()
	var out bytes.Buffer
	found, err := Sanitize(&out, strings.NewReader(input), policy)
	require.NoError(t, err)
	copied, ok := strings.CutPrefix(out.String(), doctype)
	require.True(t, ok, "no doctype first: %q", out.String())
	return copied, found
}

// sanitizeCase is an input to [Sanitize] and the copy it writes,
// without the doctype.
type sanitizeCase struct {
	name  string
	input string
	want  string
}

// attacks keep nothing that runs, loads, or submits.
var attacks = []sanitizeCase{
	{"script", `<p>a<script>alert(1)</script>b</p>`, `<p>ab</p>`},
	{"self-closing script", `<script/>alert(1)</script>x`, `x`},
	{"script text that looks like a tag", `<script><p>x</p></script>y`, `y`},
	{"event handler", `<img onerror="alert(1)" alt="x">`, `<img alt="x">`},
	{"event handler in upper case", `<P ONCLICK="alert(1)">x</P>`, `<p>x</p>`},
	{"javascript link", `<a href="javascript:alert(1)">x</a>`, `<a>x</a>`},
	{"javascript link in entities", `<a href="&#x6a;avascript:alert(1)">x</a>`, `<a>x</a>`},
	{"javascript link with a tab", `<a href="java&#9;script:alert(1)">x</a>`, `<a>x</a>`},
	{"javascript link with spaces", `<a href=" javascript:alert(1)">x</a>`, `<a>x</a>`},
	{"data link", `<a href="data:text/html,<script>alert(1)</script>">x</a>`, `<a>x</a>`},
	{"data image", `<img src="data:image/png;base64,iVBORw0KGgo=">`, `<img>`},
	{"relative link, which would reach inbok", `<a href="/api/v0/session">x</a>`, `<a>x</a>`},
	{"scheme-relative image", `<img src="//tracker.example/p.gif">`, `<img>`},
	{"link with a space", `<a href="mailto:a b@example.com">x</a><a href="https://example.com/a b">y</a>`, `<a>x</a><a>y</a>`},
	{"http link without a host", `<a href="http:x">x</a>`, `<a>x</a>`},
	{"svg", `<svg><script>alert(1)</script><a href="javascript:alert(1)">x</a></svg>y`, `y`},
	{"self-closing svg", `<svg/><p>x</p>`, `<p>x</p>`},
	{"math", `<math><mi xlink:href="javascript:alert(1)">x</mi></math>y`, `y`},
	{"nested svg", `<svg><svg></svg>x</svg>y`, `y`},
	{"form", `<form action="https://evil.example"><input name="p"><button formaction="https://evil.example">Go</button></form>`, `Go`},
	{"select", `<select><option>a</option></select>b`, `b`},
	{"textarea", `<textarea><img src=x onerror=alert(1)></textarea>b`, `b`},
	{"meta refresh", `<meta http-equiv="refresh" content="0;url=https://evil.example">x`, `x`},
	{"base", `<base href="https://evil.example/"><a href="x">y</a>`, `<a>y</a>`},
	{"link stylesheet", `<link rel="stylesheet" href="https://evil.example/a.css">x`, `x`},
	{"iframe", `<iframe src="https://evil.example"></iframe>x`, `x`},
	{"object and embed", `<object data="https://evil.example/a.swf"><param name="a" value="b">fallback</object><embed src="https://evil.example">`, `fallback`},
	{"template", `<template><img src=x onerror=alert(1)></template>y`, `y`},
	{"title", `<head><title>secret</title></head>`, `<head></head>`},
	{"comment", `<!--<img src=x onerror=alert(1)>-->x`, `x`},
	{"outlook conditional comment", `<!--[if mso]><v:rect fill="true"><v:fill src="https://evil.example/x.png"/></v:rect><![endif]-->x`, `x`},
	{"cdata, a comment to its first >", `<![CDATA[<img src=x onerror=alert(1)>]]>x`, `]]&gt;x`},
	{"plaintext drops the rest", `a<plaintext><b>b</b>`, `a`},
	{
		// The tokenizer reads noscript's content as text, as a browser
		// that runs scripts does; one that does not reads the img as
		// markup. noscript and its text are dropped either way, and the
		// img is read as the tokenizer reads it.
		"noscript",
		`<noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`,
		`<img>&#34;&gt;`,
	},
	{"text that looks like markup", `&lt;script&gt;alert(1)&lt;/script&gt;`, `&lt;script&gt;alert(1)&lt;/script&gt;`},
	{"attribute value breaking out", `<p title='"><script>alert(1)</script>'>x</p>`, `<p title="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;">x</p>`},
	{"attribute name with a quote", `<p aria-x"y="1" aria-label="z">x</p>`, `<p aria-label="z">x</p>`},
	{"data attribute", `<p data-x="1">x</p>`, `<p>x</p>`},
	{"first of two attributes", `<a href="https://example.com/" href="javascript:alert(1)">x</a>`, `<a href="https://example.com/" target="_blank" rel="noopener noreferrer">x</a>`},
	{"second of two attributes", `<p title="a" title="b" TITLE="c">x</p>`, `<p title="a">x</p>`},
	{"style element", `<style>p{color:red}</style>x`, `x`},
	{"style attribute", `<p style="color:red">x</p>`, `<p>x</p>`},
	{"srcset", `<img srcset="https://tracker.example/p.gif 1x">`, `<img>`},
	{"unknown element keeps its text", `<marquee>a</marquee><o:p>b</o:p>`, `ab`},
	{"link attribute on an element that is not a link", `<b href="https://example.com/">x</b>`, `<b>x</b>`},
	{"image attribute on an element without images", `<div src="https://tracker.example/p.gif" background="https://tracker.example/p.gif">x</div>`, `<div>x</div>`},
	{"https image without a host", `<img src="https:evil.example/x">`, `<img>`},
	{"image with another scheme", `<img src="ftp://cdn.example/a.png">`, `<img>`},
	// From Roundcube's fixes of published sanitizer bypasses.
	{"background attribute carrying css", `<body background="x;color:red">x</body>`, `<body>x</body>`},
}

func TestSanitizeAttacks(t *testing.T) {
	for _, tt := range attacks {
		t.Run(tt.name, func(t *testing.T) {
			got, found := sanitize(t, tt.input, Policy{})

			assert.Equal(t, tt.want, got)
			checkSafe(t, doctype+got, RemoteBlocked)
			assert.Empty(t, found.Shown)
		})
	}
}

// layouts keep what a message's layout needs, written again.
var layouts = []sanitizeCase{
	{"document", `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN"><html lang="en"><head></head><body bgcolor="#ffffff" text="#000">x</body></html>`, `<html lang="en"><head></head><body bgcolor="#ffffff" text="#000">x</body></html>`},
	{"table layout", `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" align="center"><tr><td valign="top" colspan="2" class="c" id="i">x</td></tr></table>`, `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" align="center"><tr><td valign="top" colspan="2" class="c" id="i">x</td></tr></table>`},
	{"presentational elements", `<center><font face="Arial" size="2" color="red">x</font></center>`, `<center><font face="Arial" size="2" color="red">x</font></center>`},
	{"link", `<a href="https://example.com/a?b=1&amp;c=2" title="t">x</a>`, `<a href="https://example.com/a?b=1&amp;c=2" title="t" target="_blank" rel="noopener noreferrer">x</a>`},
	{"link's own target", `<a href="https://example.com/" target="_top" rel="opener">x</a>`, `<a href="https://example.com/" target="_blank" rel="noopener noreferrer">x</a>`},
	{"mailto and tel links", `<a href="mailto:a@example.com?subject=Hi">a</a><a href="tel:+15551234">b</a>`, `<a href="mailto:a@example.com?subject=Hi" target="_blank" rel="noopener noreferrer">a</a><a href="tel:+15551234" target="_blank" rel="noopener noreferrer">b</a>`},
	{"self-closing void", `<br/><img alt="a" width="10" height="20"/>`, `<br><img alt="a" width="10" height="20">`},
	{"entities", `<p title="a&amp;b">&copy; &lt; &#x1F600;</p>`, `<p title="a&amp;b">© &lt; 😀</p>`},
}

func TestSanitizeLayouts(t *testing.T) {
	for _, tt := range layouts {
		t.Run(tt.name, func(t *testing.T) {
			got, found := sanitize(t, tt.input, Policy{})

			assert.Equal(t, tt.want, got)
			checkSafe(t, doctype+got, RemoteBlocked)
			assert.False(t, found.Blocked)
		})
	}
}

// cid: URLs that name an inline part become the part's URL; others show
// nothing.
func TestSanitizeInline(t *testing.T) {
	policy := Policy{
		Inline: []Part{
			{ID: "1.1", ContentID: "logo@example.com"},
			{ID: "1.2", ContentID: "a%b@example.com"},
			{ID: "1.3", ContentID: "logo@example.com"},
			{ID: "1.4"},
		},
		PartURL: partURL,
	}
	input := `<img src="cid:logo@example.com">` +
		`<img src="CID:a%25b@example.com">` +
		`<img src="cid:missing@example.com">` +
		`<table background="cid:logo@example.com"><tr><td>x</td></tr></table>` +
		`<img src="cid:">`

	got, found := sanitize(t, input, policy)

	assert.Equal(t, `<img src="/parts/1.1">`+
		`<img src="/parts/1.2">`+
		`<img>`+
		`<table background="/parts/1.1"><tr><td>x</td></tr></table>`+
		`<img>`, got)
	assert.Equal(t, []string{"1.1", "1.2"}, found.Shown)
	assert.False(t, found.Blocked)
}

// Without a part URL, a cid: URL shows nothing.
func TestSanitizeInlineNoPartURL(t *testing.T) {
	policy := Policy{Inline: []Part{{ID: "1.1", ContentID: "logo"}}}

	got, found := sanitize(t, `<img src="cid:logo">`, policy)

	assert.Equal(t, `<img>`, got)
	assert.Empty(t, found.Shown)
}

// Remote images load only when allowed; a blocked one is reported, so a
// view can offer to load it.
func TestSanitizeRemote(t *testing.T) {
	input := `<img src="https://cdn.example/a.png" alt="a">` +
		`<img src="http://cdn.example/b.png">` +
		`<img src="//cdn.example/g.png">` +
		`<td background="https://cdn.example/c.png">`

	t.Run("blocked", func(t *testing.T) {
		got, found := sanitize(t, input, Policy{Remote: RemoteBlocked})

		assert.Equal(t, `<img alt="a">`+
			`<img>`+
			`<img>`+
			`<td>`, got)
		assert.True(t, found.Blocked)
	})
	t.Run("allowed", func(t *testing.T) {
		got, found := sanitize(t, input, Policy{Remote: RemoteAllowed})

		assert.Equal(t, `<img src="https://cdn.example/a.png" alt="a">`+
			`<img src="http://cdn.example/b.png">`+
			`<img src="https://cdn.example/g.png">`+
			`<td background="https://cdn.example/c.png">`, got)
		assert.False(t, found.Blocked)
	})
	t.Run("image attribute on an element without images", func(t *testing.T) {
		policy := Policy{Inline: []Part{{ID: "1.1", ContentID: "logo"}}, PartURL: partURL}

		got, found := sanitize(t, `<div src="cid:logo" background="cid:logo">x</div>`, policy)

		assert.Equal(t, `<div>x</div>`, got)
		assert.Empty(t, found.Shown)
	})
	t.Run("zero policy blocks", func(t *testing.T) {
		got, found := sanitize(t, `<img src="https://cdn.example/a.png">`, Policy{})

		assert.Equal(t, `<img>`, got)
		assert.True(t, found.Blocked)
	})
}

// A tag with many attributes takes time in proportion to them, not to
// their square.
func TestSanitizeManyAttributes(t *testing.T) {
	var input strings.Builder
	input.WriteString("<p")
	for n := range 50_000 {
		input.WriteString(" aria-")
		for ; n > 0; n /= 26 {
			input.WriteByte(byte('a' + n%26))
		}
		input.WriteString("x")
	}
	input.WriteString(">")

	got, _ := sanitize(t, input.String(), Policy{})

	assert.Equal(t, 50_000, strings.Count(got, " aria-"))
}

// A read error stops [Sanitize] and is returned.
func TestSanitizeReadError(t *testing.T) {
	fail := errors.New("read failed")

	_, err := Sanitize(&bytes.Buffer{}, iotest.ErrReader(fail), Policy{})

	require.ErrorIs(t, err, fail)
}

// A write error is returned.
func TestSanitizeWriteError(t *testing.T) {
	fail := errors.New("write failed")

	_, err := Sanitize(failingWriter{fail}, strings.NewReader("<p>x</p>"), Policy{})

	require.ErrorIs(t, err, fail)
}

// failingWriter fails every write with err.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

// Real newsletters keep their table layout and links, lose their Outlook
// comments and style sheets, and load no remote image unless allowed.
func TestSanitizeNewsletters(t *testing.T) {
	tests := []struct {
		file        string
		wantKept    []string
		wantBlocked bool
	}{
		{
			file: "cerberus-hybrid.html",
			wantKept: []string{
				`<table align="center" role="presentation" cellspacing="0" cellpadding="0" border="0">`,
				`<a class="button-a button-a-primary" href="https://google.com/" target="_blank" rel="noopener noreferrer">`,
			},
			wantBlocked: true,
		},
		{
			file: "responsive-email.html",
			wantKept: []string{
				`<table role="presentation" border="0" cellpadding="0" cellspacing="0" class="body">`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			input, err := os.ReadFile(filepath.Join("testdata", "newsletters", tt.file))
			require.NoError(t, err)

			got, found := sanitize(t, string(input), Policy{})

			for _, kept := range tt.wantKept {
				assert.Contains(t, got, kept)
			}
			assert.NotContains(t, got, "mso")
			assert.NotContains(t, got, "<style")
			assert.NotContains(t, got, " style=")
			assert.Equal(t, tt.wantBlocked, found.Blocked)
			checkSafe(t, doctype+got, RemoteBlocked)
		})
	}
}

// checkSafe fails t unless doc, a copy [Sanitize] wrote under remote,
// parses as a browser parses it into kept HTML elements only, with the
// attributes Sanitize writes, links and images with the URLs it allows,
// and no comments, and unless [checkReference] passes it.
func checkSafe(t testing.TB, doc string, remote Remote) {
	t.Helper()
	checkReference(t, doc)
	tree, err := html.Parse(strings.NewReader(doc))
	require.NoError(t, err)
	for node := range tree.Descendants() {
		switch node.Type {
		case html.CommentNode:
			t.Errorf("comment %q", node.Data)
		case html.ElementNode:
			assert.Empty(t, node.Namespace, "element %q", node.Data)
			assert.True(t, kept[node.DataAtom], "element %q", node.Data)
			for _, attr := range node.Attr {
				checkAttribute(t, attr)
			}
		}
	}
}

// reference is bluemonday's UGCPolicy, the maintained rules of the most
// used Go HTML sanitizer, widened only by what mail needs and [Sanitize]
// keeps on purpose, each group with its reason.
var reference = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// A whole document, shown in a frame of its own.
	p.AllowElements("html", "head", "body")
	// Elements that only group or mark text.
	p.AllowElements("address", "data", "footer", "header", "kbd", "main", "nav")
	// HTML 4's presentational elements, which mail still lays out with.
	p.AllowElements("big", "center", "font", "nobr")
	// Text and layout attributes, copied with any value: UGCPolicy lacks
	// some, e.g. bgcolor, and limits others to patterns, e.g. a number
	// for width, while Sanitize escapes the value instead. Kept apart
	// from [attributes], so an attribute added there alone still fails.
	p.AllowAttrs("abbr", "align", "alink", "alt", "bgcolor", "border",
		"cellpadding", "cellspacing", "clear", "color", "colspan",
		"datetime", "dir", "face", "frame", "headers", "height", "hidden",
		"hspace", "id", "lang", "link", "noshade", "nowrap", "open",
		"reversed", "role", "rowspan", "rules", "scope", "size", "span",
		"start", "summary", "text", "title", "type", "valign", "value",
		"vlink", "vspace", "width").Globally()
	// What a newsletter's style sheet selects by.
	p.AllowAttrs("class").Globally()
	// Elements bluemonday keeps only with an attribute: a link or image
	// whose URL Sanitize dropped, showing its text or alt, and elements
	// that, bare, only hold text.
	p.AllowNoAttrs().OnElements("a", "img", "bdo", "big", "data", "font", "main", "nobr")
	p.AllowURLSchemes(schemeTel)
	return p
}()

// checkReference fails t if [reference] would remove an element or an
// attribute from doc, a copy [Sanitize] wrote, so Sanitize keeps nothing
// that bluemonday's rules, as widened for mail, do not.
func checkReference(t testing.TB, doc string) {
	t.Helper()
	ours, theirs := shape(t, doc), shape(t, reference.Sanitize(doc))
	for key, count := range ours {
		assert.LessOrEqual(t, count, theirs[key], "bluemonday removes %s from %q", key, doc)
	}
}

// shape counts the elements of doc, an HTML document, and their
// attributes, by name, e.g. "img" and "img[src]". It leaves out what
// Sanitize writes itself or checks by name: target, rel, and aria-*
// attributes.
func shape(t testing.TB, doc string) map[string]int {
	t.Helper()
	tree, err := html.Parse(strings.NewReader(doc))
	require.NoError(t, err)
	counts := map[string]int{}
	for node := range tree.Descendants() {
		if node.Type != html.ElementNode {
			continue
		}
		counts[node.Data]++
		for _, attr := range node.Attr {
			if attr.Key == "target" || attr.Key == "rel" || isAria(attr.Key) {
				continue
			}
			counts[node.Data+"["+attr.Key+"]"]++
		}
	}
	return counts
}

// checkAttribute fails t unless attr is one [Sanitize] may write.
func checkAttribute(t testing.TB, attr html.Attribute) {
	t.Helper()
	assert.Empty(t, attr.Namespace, "attribute %q", attr.Key)
	switch attr.Key {
	case attrHref:
		u, err := url.Parse(attr.Val)
		require.NoError(t, err)
		assert.Contains(t, []string{schemeHTTP, schemeHTTPS, schemeMailto, schemeTel}, u.Scheme, "href %q", attr.Val)
	case attrSrc, attrBackground:
		u, err := url.Parse(attr.Val)
		require.NoError(t, err)
		local := strings.HasPrefix(attr.Val, partURL(""))
		assert.True(t, local || u.Scheme == schemeHTTP || u.Scheme == schemeHTTPS, "%s %q", attr.Key, attr.Val)
	case "target", "rel":
	default:
		assert.True(t, attributes[attr.Key] || isAria(attr.Key), "attribute %q", attr.Key)
	}
}

// Any input becomes a copy that is safe and that [Sanitize] copies again
// unchanged, when the copy has no inline parts: a second copy drops the
// relative part URLs the first wrote for cid: URLs.
func FuzzSanitize(f *testing.F) {
	for _, tt := range slices.Concat(attacks, layouts) {
		f.Add(tt.input)
	}
	policy := Policy{
		Inline:  []Part{{ID: "1.1", ContentID: "logo"}},
		PartURL: partURL,
	}
	f.Fuzz(func(t *testing.T, input string) {
		blocked, _ := sanitize(t, input, policy)
		checkSafe(t, doctype+blocked, RemoteBlocked)

		// cid: URLs become relative part URLs, which a second copy drops.
		allowed, _ := sanitize(t, input, Policy{Remote: RemoteAllowed})
		checkSafe(t, doctype+allowed, RemoteAllowed)
		again, _ := sanitize(t, doctype+allowed, Policy{Remote: RemoteAllowed})
		assert.Equal(t, allowed, again, "a second copy differs")
	})
}
