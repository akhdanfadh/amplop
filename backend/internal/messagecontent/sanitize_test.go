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
	cssinput "github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
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
	{"relative link, which would reach amplop", `<a href="/api/v0/session">x</a>`, `<a>x</a>`},
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
	{"srcset", `<img srcset="https://tracker.example/p.gif 1x">`, `<img>`},
	{"unknown element keeps its text", `<marquee>a</marquee><o:p>b</o:p>`, `ab`},
	{"style breaking out of its element", `<style>a{font-family:"</style><script>alert(1)</script>"}</style>x`, `<style>a{}</style>&#34;}x`},
	{"style's attributes", `<style media="screen" type="TEXT/CSS" onload="alert(1)">p{color:red}</style>`, `<style media="screen">p{color:red;}</style>`},
	{"style for print only", `<style media="print">p{display:none}</style><p>x</p>`, `<p>x</p>`},
	{"style for print and screen", `<style media="print, screen">p{color:red}</style>`, `<style media="screen">p{color:red;}</style>`},
	{"style for only screen", `<style media="only screen">p{color:red}</style>`, `<style media="only screen">p{color:red;}</style>`},
	{"style for narrow screens", `<style media="screen and (max-width:600px)">p{color:red}</style>`, `<style media="screen and ( max-width : 600px )">p{color:red;}</style>`},
	{"style for print or narrow screens", `<style media="print, (max-width:600px)">p{color:red}</style>`, `<style media="( max-width : 600px )">p{color:red;}</style>`},
	{"style for a feature not kept", `<style media="screen and (hover:hover)">p{color:red}</style><p>x</p>`, `<p>x</p>`},
	{"style media breaking out", `<style media='screen"><script>alert(1)</script>'>p{color:red}</style>x`, `x`},
	{"style that is not css", `<style type="text/plain">p{display:none}</style><p>x</p>`, `<p>x</p>`},
	{"link attribute on an element that is not a link", `<b href="https://example.com/">x</b>`, `<b>x</b>`},
	{"image attribute on an element without images", `<div src="https://tracker.example/p.gif" background="https://tracker.example/p.gif">x</div>`, `<div>x</div>`},
	{"style inside a dropped element", `<svg><style>p{color:red}</style></svg>x`, `x`},
	{"css expression", `<p style="width:expression(alert(1));color:red">x</p>`, `<p style="color:red;">x</p>`},
	{"css behavior", `<p style="behavior:url(x.htc);-moz-binding:url(x.xml#x)">x</p>`, `<p>x</p>`},
	{"css position", `<p style="position:fixed!important;top:0;z-index:9999">x</p>`, `<p>x</p>`},
	{"css import", `<style>@import url("https://evil.example/a.css");p{color:red}</style>`, `<style>p{color:red;}</style>`},
	{"css font face", `<style>@font-face{font-family:x;src:url(https://evil.example/x.woff)}p{color:red}</style>`, `<style>p{color:red;}</style>`},
	{"css var fallback", `<p style="background-image:image-set(var(--x,'//evil.example/p.gif'));color:red">x</p>`, `<p style="color:red;">x</p>`},
	{"css custom property", `<p style="--x:url(https://evil.example/p.gif);color:red">x</p>`, `<p style="color:red;">x</p>`},
	{"css escaped url", `<p style="background:url(/\5c/evil.example/p.gif)">x</p>`, `<p>x</p>`},
	{"css escaped name", `<p style="backgr\6f und:url(https://evil.example/p.gif)">x</p>`, `<p>x</p>`},
	{"css less-than in a string", `<style>p{font-family:"a<b"}</style>`, `<style>p{}</style>`},
	{"css comment tokens", `<style><!-- p{color:red} --></style>`, `<style>p{color:red;}</style>`},
	{"css unbalanced function", `<p style="color:rgb(1,2,3">x</p>`, `<p>x</p>`},
	{"css escaped selector", `<style>.x\31 {color:red}p{color:blue}</style>`, `<style>p{color:blue;}</style>`},
	{"css attribute selector", `<style>input[value^="a"]{color:red}p{color:blue}</style>`, `<style>p{color:blue;}</style>`},
	{"css comment opened across a dropped space", `<style>p{width:1/ *2;font-family:"*/;background:url(https://evil.example/x);x:"}</style>`, `<style>p{width:1 / * 2;font-family:"*/;background:url(https://evil.example/x);x:";}</style>`},
	{"css comment opened across a dropped space, inline", `<p style="width:1/ *;font-family:&quot;*/;background:url(https://evil.example/x);x:&quot;">x</p>`, `<p style="width:1 / *;font-family:&#34;*/;background:url(https://evil.example/x);x:&#34;;">x</p>`},
	{"css names joined across a comment", `<style>@media/**/screen{p/**/q{color:red}p{color:blue}}</style>`, `<style>@media screen{p{color:blue;}}</style>`},
	{"css url joined to the next function", `<p style="background:URL()linear-gradient(red,blue)">x</p>`, `<p style="background:none linear-gradient( red , blue );">x</p>`},
	{"css token type outside the list", `<p style="font-family:[x];color:red">x</p>`, `<p style="color:red;">x</p>`},
	{"css escape in a string", `<p style='font-family:"a\62";color:red'>x</p>`, `<p style="color:red;">x</p>`},
	{"css delimiter outside the list", `<p style="font-family:a=b;color:red">x</p>`, `<p style="color:red;">x</p>`},
	{"css selector delimiter outside the list", `<style>p&q{color:red}p{color:blue}</style>`, `<style>p{color:blue;}</style>`},
	{"css media query with a string", `<style>@media screen and "x"{p{color:red}}</style>`, ``},
	{"https image without a host", `<img src="https:evil.example/x">`, `<img>`},
	{"image with another scheme", `<img src="ftp://cdn.example/a.png">`, `<img>`},
	{"text after an empty style", `<style></style>hello`, `hello`},
	// Payloads from published email sanitizer bypasses: PortSwigger's
	// "CSS: the bomb inside your inbox" (2026), Proton's and Roundcube's
	// fixes, and Zimbra's CVE-2025-66376.
	{"css percentage joined to a url", `<p style="background:0%url(/foo);color:red">x</p>`, `<p style="background:0% none;color:red;">x</p>`},
	{"css url left open", `<p style="color:red;background:url(https://evil.example/x">x</p>`, `<p style="color:red;background:none;">x</p>`},
	{"css import after a broken media rule", `<style>@media --x;@import'//evil.example/x';p{color:red}</style>`, `<style>p{color:red;}</style>`},
	{"css escape before a line break in a url", "<p style=\"background:url(ht\\74\r\ntp://evil.example/x);color:red\">x</p>", `<p style="color:red;">x</p>`},
	{"background attribute carrying css", `<body background="x;color:red">x</body>`, `<body>x</body>`},
	{"letter joined to a url", `<p style="background:éurl(https://evil.example/x);color:red">x</p>`, `<p style="color:red;">x</p>`},
	{"css names with a letter outside ASCII", `<style>@media (orİentation:portraİt){p{color:red}}</style><p style="background:lİnear-gradient(red,blue);colİr:blue;COLOR:red">x</p>`, `<p style="color:red;">x</p>`},
	{"css pseudo-class", `<style>a:visited{color:red}p:has(a){color:blue}</style>`, ``},
}

func TestSanitizeAttacks(t *testing.T) {
	for _, tt := range attacks {
		t.Run(tt.name, func(t *testing.T) {
			got, found := sanitize(t, tt.input, Policy{})

			assert.Equal(t, tt.want, got)
			assertSafe(t, doctype+got, RemoteBlocked)
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
	{"hidden preheader", `<div style="display:none;max-height:0;overflow:hidden" aria-hidden="true">preview</div>`, `<div style="display:none;max-height:0;overflow:hidden;" aria-hidden="true">preview</div>`},
	{"css values", `<p style="font:12px/1.5 'Helvetica Neue',Arial,sans-serif;color:#333!important;width:calc(100% - 2px);background:linear-gradient(rgba(0,0,0,.5),#fff)">x</p>`, `<p style="font:12px / 1.5 &#39;Helvetica Neue&#39; , Arial , sans-serif;color:#333 ! important;width:calc( 100% - 2px );background:linear-gradient( rgba( 0 , 0 , 0 , .5 ) , #fff );">x</p>`},
	{"css media query", `<style>@media only screen and (max-width: 600px), screen and (orientation: portrait) { .a, td > p { width: 100% !important } }</style>`, `<style>@media only screen and ( max-width : 600px ) , screen and ( orientation : portrait ){.a,td>p{width:100% ! important;}}</style>`},
	{"css media query Gmail lacks", `<style>@media print{p{color:red}}@media (prefers-color-scheme: dark){p{color:white}}p{color:blue}</style>`, `<style>p{color:blue;}</style>`},
	{"css nested media", `<style>@media screen{@media (min-width:1px){p{color:red}}p{color:blue}}</style>`, `<style>@media screen{p{color:blue;}}</style>`},
	{"css selectors", `<style>#a .b>c+d~e *,body{margin:0}</style>`, `<style>#a .b>c+d~e *,body{margin:0;}</style>`},
	{"css viewport heights", `<p style="min-height:100vh;height:calc(50DVH - 2px);max-height:10vmin;padding:1e1cqh;width:50vw;margin:0">x</p>`, `<p style="width:50vw;margin:0;">x</p>`},
	{"css viewport heights in vertical writing", `<p style="writing-mode:vertical-rl;height:100vi;width:30vb;padding:1svi 2cqi">x</p>`, `<p style="writing-mode:vertical-rl;">x</p>`},
	{"css property case", `<p style="COLOR:Red">x</p>`, `<p style="color:Red;">x</p>`},
	{"unclosed style", `<style>p{color:red`, `<style>p{color:red;}</style>`},
	{"unclosed media", `<style>@media screen{p{color:red`, `<style>@media screen{p{color:red;}}</style>`},
	{"unclosed media after an error", `<style>@media{)`, `<style>@media {}</style>`},
	{"css after an error", `<style>p{color:red;broken;background:blue}div{color:green}</style><p style="color:red;broken;background:blue">x</p>`, `<style>p{color:red;background:blue;}div{color:green;}</style><p style="color:red;background:blue;">x</p>`},
	{"css resolution query", `<style>@media (min-resolution:2dppx) and (max-resolution:3dppx){p{color:red}}</style>`, `<style>@media ( min-resolution : 2dppx ) and ( max-resolution : 3dppx ){p{color:red;}}</style>`},
}

func TestSanitizeLayouts(t *testing.T) {
	for _, tt := range layouts {
		t.Run(tt.name, func(t *testing.T) {
			got, found := sanitize(t, tt.input, Policy{})

			assert.Equal(t, tt.want, got)
			assertSafe(t, doctype+got, RemoteBlocked)
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
		`<table background="cid:logo@example.com"><tr><td style="background:#fff url('cid:a%25b@example.com') no-repeat">x</td></tr></table>` +
		`<img src="cid:">`

	got, found := sanitize(t, input, policy)

	assert.Equal(t, `<img src="/parts/1.1">`+
		`<img src="/parts/1.2">`+
		`<img>`+
		`<table background="/parts/1.1"><tr><td style="background:#fff url(&#34;/parts/1.2&#34;) no-repeat;">x</td></tr></table>`+
		`<img>`, got)
	assert.Equal(t, []string{"1.1", "1.2"}, found.Shown)
	assert.False(t, found.Blocked)
}

// A url() in a kept declaration shows its inline part, or blocks its
// remote image, as an img would.
func TestSanitizeCSSImages(t *testing.T) {
	policy := Policy{Inline: []Part{{ID: "1.1", ContentID: "logo"}}, PartURL: partURL}
	input := `<p style="background:url(cid:logo)">x</p>` +
		`<style>p{background:url(https://cdn.example/a.png)}</style>`

	got, found := sanitize(t, input, policy)

	assert.Equal(t, `<p style="background:url(&#34;/parts/1.1&#34;);">x</p><style>p{background:none;}</style>`, got)
	assert.Equal(t, []string{"1.1"}, found.Shown)
	assert.True(t, found.Blocked)
}

// A url() in a declaration that is dropped shows no inline part and
// blocks no remote image, so the part stays a download and no remote
// content is offered.
func TestSanitizeDroppedDeclaration(t *testing.T) {
	policy := Policy{Inline: []Part{{ID: "1.1", ContentID: "logo"}}, PartURL: partURL}
	input := `<p style="background:url(cid:logo) var(--x)">x</p>` +
		`<style>p{background:url(https://cdn.example/a.png) expression(x)}</style>`

	got, found := sanitize(t, input, policy)

	assert.Equal(t, `<p>x</p><style>p{}</style>`, got)
	assert.Empty(t, found.Shown)
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
		`<td background="https://cdn.example/c.png">` +
		`<p style="background:#fff url(https://cdn.example/d.png) no-repeat;list-style:url(&quot;https://cdn.example/e.png&quot;) disc">x</p>` +
		`<style>p{background-image:url(https://cdn.example/f.png)}</style>`

	t.Run("blocked", func(t *testing.T) {
		got, found := sanitize(t, input, Policy{Remote: RemoteBlocked})

		assert.Equal(t, `<img alt="a">`+
			`<img>`+
			`<img>`+
			`<td>`+
			`<p style="background:#fff none no-repeat;list-style:none disc;">x</p>`+
			`<style>p{background-image:none;}</style>`, got)
		assert.True(t, found.Blocked)
	})
	t.Run("allowed", func(t *testing.T) {
		got, found := sanitize(t, input, Policy{Remote: RemoteAllowed})

		assert.Equal(t, `<img src="https://cdn.example/a.png" alt="a">`+
			`<img src="http://cdn.example/b.png">`+
			`<img src="https://cdn.example/g.png">`+
			`<td background="https://cdn.example/c.png">`+
			`<p style="background:#fff url(&#34;https://cdn.example/d.png&#34;) no-repeat;list-style:url(&#34;https://cdn.example/e.png&#34;) disc;">x</p>`+
			`<style>p{background-image:url("https://cdn.example/f.png");}</style>`, got)
		assert.False(t, found.Blocked)
	})
	t.Run("css url with parentheses and quotes", func(t *testing.T) {
		got, _ := sanitize(t, `<p style="background:url('https://cdn.example/a(1).png')">x</p><style>p{background:url('https://cdn.example/a.png?b="1')}</style>`, Policy{Remote: RemoteAllowed})

		// A quote in a query stays one, which would end the string.
		assert.Equal(t, `<p style="background:url(&#34;https://cdn.example/a(1).png&#34;);">x</p><style>p{background:none;}</style>`, got)
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

// CSS past [maxCSS] in one document is dropped, a style sheet or
// attribute at a time, and what still fits is kept.
func TestSanitizeCSSBudget(t *testing.T) {
	big := `<p style="color:red;` + strings.Repeat(" ", maxCSS) + `">x</p>`
	small := `<p style="color:blue">y</p>`
	// The two style attributes take 10 bytes each and the sheet 14 plus
	// its padding, so the sheet fits and the last attribute does not.
	sheet := `<style>p{color:green}` + strings.Repeat(" ", maxCSS-28) + `</style>`

	got, _ := sanitize(t, big+small+sheet+small, Policy{})

	assert.Equal(t, `<p>x</p><p style="color:blue;">y</p><style>p{color:green;}</style><p>y</p>`, got)
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

// Real newsletters keep their responsive CSS and table layout, lose
// their Outlook comments, and load no remote image unless allowed.
func TestSanitizeNewsletters(t *testing.T) {
	tests := []struct {
		file        string
		wantKept    []string
		wantBlocked bool
	}{
		{
			file: "cerberus-hybrid.html",
			wantKept: []string{
				`@media screen and ( max-width : 480px ){.stack-column,.stack-column-center{display:block ! important;width:100% ! important;`,
				`<table align="center" role="presentation" cellspacing="0" cellpadding="0" border="0" style="margin:auto;">`,
				`<td style="text-align:center;background-image:none;background-color:#222222;`,
				`<a class="button-a button-a-primary" href="https://google.com/" style="background:#222222;`,
			},
			wantBlocked: true,
		},
		{
			file: "responsive-email.html",
			wantKept: []string{
				`@media only screen and ( max-width : 640px ){.main p,.main td,.main span{font-size:16px ! important;}`,
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
			assert.Equal(t, tt.wantBlocked, found.Blocked)
			assertSafe(t, doctype+got, RemoteBlocked)
		})
	}
}

// assertSafe fails t unless doc, a copy [Sanitize] wrote under remote,
// parses as a browser parses it into kept HTML elements and style
// elements only, with the attributes Sanitize writes, links and images
// with the URLs it allows, CSS that [assertCSS] passes, and no comments,
// and unless [assertReference] passes it.
func assertSafe(t testing.TB, doc string, remote Remote) {
	t.Helper()
	assertReference(t, doc)
	tree, err := html.Parse(strings.NewReader(doc))
	require.NoError(t, err)
	for node := range tree.Descendants() {
		switch node.Type {
		case html.CommentNode:
			t.Errorf("comment %q", node.Data)
		case html.TextNode:
			if node.Parent.DataAtom == atom.Style {
				assert.NotContains(t, node.Data, "<", "style element")
				assertCSS(t, node.Data, remote)
			}
		case html.ElementNode:
			assert.Empty(t, node.Namespace, "element %q", node.Data)
			assert.True(t, kept[node.DataAtom] || node.DataAtom == atom.Style, "element %q", node.Data)
			for _, attr := range node.Attr {
				// A style element's one attribute, its media queries.
				if node.DataAtom == atom.Style {
					assert.Equal(t, attrMedia, attr.Key, "style attribute")
					assertCSS(t, "@media "+attr.Val+"{}", remote)
					continue
				}
				assertAttribute(t, attr)
				if attr.Key == attrStyle {
					assertCSS(t, attr.Val, remote)
				}
			}
		}
	}
}

// assertCSS fails t unless a CSS tokenizer reads src, CSS [Sanitize]
// wrote under remote, without comments or bad strings and URLs, with no
// at-rule but @media, no function outside [functions], and no url() but
// an inline part's or, under [RemoteAllowed], a remote one.
//
// DESIGN: it tokenizes with the filter's own lexer, as Go has no other,
// so it cannot see where that lexer and a browser's differ; the security
// reviews checked that by running the copies in Chromium.
func assertCSS(t testing.TB, src string, remote Remote) {
	t.Helper()
	lexer := css.NewLexer(cssinput.NewInputString(src))
	for {
		token, data := lexer.Next()
		text := string(data)
		switch token {
		case css.ErrorToken:
			return
		case css.CommentToken, css.CDOToken, css.CDCToken, css.BadStringToken, css.BadURLToken:
			t.Errorf("%s %q in %q", token, text, src)
		case css.AtKeywordToken:
			assert.Equal(t, "@media", text, "at-rule in %q", src)
		case css.FunctionToken:
			assert.True(t, functions[strings.ToLower(text)], "function %q in %q", text, src)
		case css.URLToken:
			local := strings.HasPrefix(text, `url("`+partURL(""))
			remoteURL := remote == RemoteAllowed && (strings.HasPrefix(text, `url("http://`) || strings.HasPrefix(text, `url("https://`))
			assert.True(t, local || remoteURL, "url %q in %q", text, src)
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
	// What a newsletter's CSS selects by and says; assertCSS judges CSS.
	p.AllowAttrs("class", "style").Globally()
	// Elements bluemonday keeps only with an attribute: a link or image
	// whose URL Sanitize dropped, showing its text or alt, and elements
	// that, bare, only hold text.
	p.AllowNoAttrs().OnElements("a", "img", "bdo", "big", "data", "font", "main", "nobr")
	p.AllowURLSchemes(schemeTel)
	return p
}()

// assertReference fails t if [reference] would remove an element or an
// attribute from doc, a copy [Sanitize] wrote, so Sanitize keeps nothing
// that bluemonday's rules, as widened for mail, do not.
func assertReference(t testing.TB, doc string) {
	t.Helper()
	ours, theirs := shape(t, doc), shape(t, reference.Sanitize(doc))
	for key, count := range ours {
		assert.LessOrEqual(t, count, theirs[key], "bluemonday removes %s from %q", key, doc)
	}
}

// shape counts the elements of doc, an HTML document, and their
// attributes, by name, e.g. "img" and "img[src]". It leaves out what
// [assertCSS] judges, style elements, and what Sanitize writes itself or
// checks by name: target, rel, and aria-* attributes.
func shape(t testing.TB, doc string) map[string]int {
	t.Helper()
	tree, err := html.Parse(strings.NewReader(doc))
	require.NoError(t, err)
	counts := map[string]int{}
	for node := range tree.Descendants() {
		if node.Type != html.ElementNode || node.DataAtom == atom.Style {
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

// assertAttribute fails t unless attr is one [Sanitize] may write.
func assertAttribute(t testing.TB, attr html.Attribute) {
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
	case "target", "rel", attrStyle:
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
		assertSafe(t, doctype+blocked, RemoteBlocked)

		// cid: URLs become relative part URLs, which a second copy drops.
		allowed, _ := sanitize(t, input, Policy{Remote: RemoteAllowed})
		assertSafe(t, doctype+allowed, RemoteAllowed)
		again, _ := sanitize(t, doctype+allowed, Policy{Remote: RemoteAllowed})
		assert.Equal(t, allowed, again, "a second copy differs")
	})
}
