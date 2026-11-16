package search

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/html/atom"
)

func TestBodyText(t *testing.T) {
	cases := map[string]struct {
		parts []TextPart
		want  string
	}{
		"plain": {
			[]TextPart{{FormatPlain, "Hello Asha,\n\nThe invoice is attached.\n"}},
			"Hello Asha, The invoice is attached.",
		},
		// The caller passes what a view shows, e.g. HTML and then a plain
		// footer in multipart/mixed: each counts, in order.
		"every part in order": {
			[]TextPart{{FormatPlain, "First part."}, {FormatHTML, "<p>Second</p>"}, {FormatPlain, "Footer."}},
			"First part. Second Footer.",
		},
		"a blank part adds nothing": {
			[]TextPart{{FormatPlain, " \r\n\t"}, {FormatHTML, "<p>Hello</p>"}},
			"Hello",
		},
		"every HTML part": {
			[]TextPart{{FormatHTML, "<p>One</p>"}, {FormatHTML, "<p>Two</p>"}},
			"One Two",
		},
		"unknown format": {
			[]TextPart{{0, "Unlabelled"}},
			"",
		},
		"none": {nil, ""},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, BodyText(c.parts), name)
	}
}

// Text from HTML keeps what a reader sees, not markup, scripts, or
// styles, and keeps words apart where blocks end.
func TestBodyTextHTML(t *testing.T) {
	cases := map[string]string{
		`<html><head><style>p { color: red }</style></head><body><p>Visible</p></body></html>`: "Visible",
		`<p>Before</p><script>var secret = "x";</script><p>After</p>`:                          "Before After",
		`<p>One</p><p>Two</p><div>Three</div><br>Four<li>Five</li>`:                            "One Two Three Four Five",
		`<table><tr><td>Cell</td><td>Next</td></tr></table>`:                                   "Cell Next",
		// Inline elements sit inside a word as often as between words.
		`<b>Bold</b>face and <a href="https://example.com">a link</a>`: "Boldface and a link",
		`Fish &amp; chips &lt;3 &eacute;clair`:                         "Fish & chips <3 éclair",
		`<!-- a comment --><p>Text</p>`:                                "Text",
		// Newsletters pad their preview text with non-breaking spaces and
		// zero-width characters; a zero-width space alone parts words.
		"<p>Sale&nbsp;today&zwnj;\u00ad!</p>":                          "Sale today!",
		"<p>first&#8203;second</p>":                                    "first second",
		"Preview&#847;&zwnj;&nbsp;&#847;&zwnj;&nbsp;\ufe0f<p>Body</p>": "Preview Body",
		`<p>Unclosed <b>tags <i>still`:                                 "Unclosed tags still",
		`<img src="x.png" alt="Logo"><p>Text</p>`:                      "Text",
		`<p>Before<img src="x.png">after</p>`:                          "Before after",
		// An unclosed head ends where the body starts, as the parser ends
		// it: at a tag that cannot stand in a head, or at visible text.
		`<head><title>Subject</title><body><p>Body`:         "Body",
		`<head><title>Subject</title><p>Body`:               "Body",
		`<head><meta charset="utf-8">Body text`:             "Body text",
		`<main>Main</main><figure>Figure</figure>`:          "Main Figure",
		`<nav>Menu</nav><h2>Title</h2><dd>Term</dd>`:        "Menu Title Term",
		`<template><p>Later</p></template><p>Now</p>`:       "Now",
		`Before<fieldset>Invoice</fieldset>After`:           "Before Invoice After",
		`<details><summary>Summary</summary>Body</details>`: "Summary Body",
		// hidden hides an element and its content, and a hidden element
		// takes no space, so the words beside it join.
		`<p>Shown</p><p hidden>Hidden <b>nested</b></p><p>Again</p>`: "Shown Again",
		`a<span hidden>x</span>b`:                                    "ab",
		`a<div hidden>x</div>b`:                                      "ab",
		`a<video hidden>x</video>b`:                                  "ab",
		// Hiding ends with the hidden element, found as the nearest open
		// element of its name, even when the end tag closes an ancestor.
		`<div hidden><div>x</div>y</div>z`: "z",
		`<div><p hidden>x</div>y`:          "y",
		// A self-closing tag on a non-void element starts it, as the
		// parser reads it, so a raw-text element still hides its text.
		`<span hidden/>x</span>After`:                                     "After",
		`Before <script/>var secret = 1;</script> After`:                  "Before After",
		`Before <style />p { color: red }</style> After`:                  "Before After",
		`Before <TITLE/>Title</TITLE> After`:                              "Before After",
		`Before <noscript/><b>x</b></noscript> After`:                     "Before After",
		`Before <iframe/>Fallback</iframe> After`:                         "Before After",
		`Visible<iframe><p>Hidden</p></iframe>after`:                      "Visible after",
		`<iframe><script>alert(1)</script>keyword</iframe><p>Visible</p>`: "Visible",
		`<p>Hi</p><audio controls>Your client cannot play audio.</audio>`: "Hi",
		`<video><source src="v.mp4">No video</video>Caption`:              "Caption",
		// The tokenizer reads noscript's content as raw text, so it is
		// hidden rather than shown as markup.
		`<noscript><p>No scripts</p></noscript><p>Text</p>`: "Text",
		// An end tag with no element of its name open closes nothing.
		`<div><p>One</span></p>Two</div>`: "One Two",
	}
	for html, want := range cases {
		assert.Equal(t, want, BodyText([]TextPart{{FormatHTML, html}}), html)
	}
}

// Past [maxDepth] open elements, text is still read; only elements are
// no longer followed. HTML lets authors leave out </p>, </li>, and </td>,
// so ordinary markup can pass the depth.
func TestBodyTextDeepHTML(t *testing.T) {
	var unclosed strings.Builder
	for n := range 700 {
		unclosed.WriteString("<p>para" + strconv.Itoa(n) + " ")
	}
	deep := "Top " + strings.Repeat("<div>", 100_000) + "Deep"

	paragraphs := strings.Fields(BodyText([]TextPart{{FormatHTML, unclosed.String()}}))
	nested := BodyText([]TextPart{{FormatHTML, deep}})

	// The cap's own depth: a hidden element hides all of its text just
	// within it, and only its first text token just past it.
	hiddenText := "<div hidden>x<b>y</b></div>"
	within := BodyText([]TextPart{{FormatHTML, strings.Repeat("<div>", maxDepth-1) + hiddenText}})
	past := BodyText([]TextPart{{FormatHTML, strings.Repeat("<div>", maxDepth) + hiddenText}})
	// Past the cap, a script still hides its text.
	script := BodyText([]TextPart{{FormatHTML, strings.Repeat("<p>", 600) + "<script>var secret = 1;</script>end"}})

	assert.Len(t, paragraphs, 700)
	assert.Equal(t, "para699", paragraphs[699])
	assert.Equal(t, "Top Deep", nested)
	assert.Empty(t, within)
	assert.Equal(t, "y", past)
	assert.Equal(t, "end", script)
}

// allocated returns the bytes run allocates.
func allocated(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// HTML crafted against a parser that repairs markup costs the tokenizer no
// more than its size. It guards against reading mail with html.Parse
// again: in golang.org/x/net v0.62.0, html.Parse took over 500 MB for
// 32 KiB of the first and quadratic time for the second.
func TestBodyTextCraftedHTML(t *testing.T) {
	var formatting strings.Builder
	formatting.WriteString("<p>")
	for n := range 480 {
		formatting.WriteString(`<b a="` + strconv.Itoa(n) + `">`)
	}
	for formatting.Len() < maxInput {
		formatting.WriteString("<p>x")
	}
	var attributes strings.Builder
	attributes.WriteString("<html")
	for n := 0; attributes.Len() < maxInput/2; n++ {
		attributes.WriteString(" a" + strconv.Itoa(n))
	}
	attributes.WriteString("><body>")
	for attributes.Len() < maxInput {
		attributes.WriteString("<html x>")
	}

	formattingCost := allocated(func() { BodyText([]TextPart{{FormatHTML, formatting.String()}}) })
	attributesCost := allocated(func() { BodyText([]TextPart{{FormatHTML, attributes.String()}}) })

	assert.Less(t, formattingCost, uint64(50<<20))
	assert.Less(t, attributesCost, uint64(50<<20))
}

// Only the first [maxInput] bytes of an HTML part are read.
func TestBodyTextLongHTML(t *testing.T) {
	padding := "<p>" + strings.Repeat("<b></b>", maxInput/7) + "</p>"
	long := "<p>Early</p>" + padding + "<p>Late</p>"

	got := BodyText([]TextPart{{FormatHTML, long}})

	assert.Equal(t, "Early", got)
}

// Neither a huge plain part nor many large HTML parts cost much more than
// the bytes the budget lets through.
func TestBodyTextBudget(t *testing.T) {
	huge := strings.Repeat("invoice ", 2<<20)
	html := "<p>" + strings.Repeat("invoice ", maxInput/8-1) + "</p>"
	many := make([]TextPart, 16)
	for n := range many {
		many[n] = TextPart{FormatHTML, html}
	}

	plainCost := allocated(func() { BodyText([]TextPart{{FormatPlain, huge}}) })
	htmlCost := allocated(func() { BodyText(many) })

	assert.Less(t, plainCost, uint64(1<<20))
	assert.Less(t, htmlCost, uint64(10<<20))
}

// At most [maxParts] parts are read, and at most [maxInput] bytes of them,
// so many parts, or text that prints nothing, cost no more than a few.
func TestBodyTextParts(t *testing.T) {
	plain := make([]TextPart, maxParts+1)
	for n := range plain {
		plain[n] = TextPart{FormatPlain, "part" + strconv.Itoa(n)}
	}
	big := "<p>" + strings.Repeat("<b></b>", maxInput/7)
	html := []TextPart{{FormatHTML, big}, {FormatHTML, "<p>Second</p>"}}

	// Plain text that prints nothing, then a word past the budget.
	invisible := strings.Repeat("\u034f", maxInput) + "late"

	read := strings.Fields(BodyText(plain))
	second := BodyText(html)
	invisibleCost := allocated(func() { assert.Empty(t, BodyText([]TextPart{{FormatPlain, invisible}})) })

	assert.Len(t, read, maxParts)
	assert.Equal(t, "part"+strconv.Itoa(maxParts-1), read[len(read)-1])
	// The first part used the message's whole HTML budget.
	assert.NotContains(t, second, "Second")
	assert.Less(t, invisibleCost, uint64(1<<20))
}

// Every element in each of the HTML Standard's lists does what its list
// says, alone, so no neighbour hides a missing one.
func TestBodyTextElements(t *testing.T) {
	read := func(html string) string { return BodyText([]TextPart{{FormatHTML, html}}) }
	// voids hold no text, so a test can only check the gap they leave.
	voids := elements(atom.Area, atom.Base, atom.Basefont, atom.Br, atom.Col, atom.Embed, atom.Hr, atom.Img, atom.Link, atom.Meta, atom.Param)
	// The lists again, from the HTML Standard's rendering section rather
	// than from the code, so the test fails when an element goes missing.
	cases := []struct {
		list      []atom.Atom
		text, gap string // with b inside, and for a void element
	}{
		{[]atom.Atom{ // display: block, list-item, and table parts; br
			atom.Address, atom.Blockquote, atom.Center, atom.Dialog, atom.Div, atom.Figure,
			atom.Figcaption, atom.Footer, atom.Form, atom.Header, atom.Hr, atom.Legend,
			atom.Listing, atom.Main, atom.P, atom.Pre, atom.Search, atom.Xmp,
			atom.Article, atom.Aside, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
			atom.Hgroup, atom.Nav, atom.Section, atom.Dir, atom.Dd, atom.Dl, atom.Dt,
			atom.Menu, atom.Ol, atom.Ul, atom.Li, atom.Table, atom.Caption, atom.Colgroup,
			atom.Col, atom.Thead, atom.Tbody, atom.Tfoot, atom.Tr, atom.Td, atom.Th,
			atom.Fieldset, atom.Details, atom.Summary, atom.Br,
		}, "a b c", "a c"},
		{[]atom.Atom{ // replaced elements
			atom.Audio, atom.Embed, atom.Iframe, atom.Img, atom.Video,
		}, "a c", "a c"},
		{[]atom.Atom{ // display: none, and noscript
			atom.Area, atom.Base, atom.Basefont, atom.Datalist, atom.Link, atom.Meta,
			atom.Noembed, atom.Noframes, atom.Param, atom.Rp, atom.Script, atom.Style,
			atom.Template, atom.Title, atom.Noscript,
		}, "ac", "ac"},
	}

	for _, c := range cases {
		for _, element := range c.list {
			name := element.String()
			if voids[element] {
				assert.Equal(t, c.gap, read("a<"+name+">c"), name)
				continue
			}
			assert.Equal(t, c.text, read("a<"+name+">b</"+name+">c"), name)
		}
	}
	// plaintext never ends: everything after it, end tags too, is its text.
	assert.Equal(t, "a b</plaintext>c", read("a<plaintext>b</plaintext>c"))
	assert.Equal(t, "c", read("<head><title>a</title></head><body>c"))
}

// Controls print nothing, so they are dropped, and a plain part holding
// only controls has no text.
func TestBodyTextControls(t *testing.T) {
	colored := BodyText([]TextPart{{FormatPlain, "Hello\x1b[31mRED\x00\x07\x7f"}})
	empty := BodyText([]TextPart{{FormatPlain, "\x00\x01"}, {FormatHTML, "<p>Invoice</p>"}})

	assert.Equal(t, "Hello[31mRED", colored)
	assert.Equal(t, "Invoice", empty)
}

// Text past the cap is cut at the last whole word, and the result is
// always valid UTF-8.
func TestBodyTextCap(t *testing.T) {
	// 9 bytes, which do not divide the cap, so it falls inside a word.
	word := "invoices "
	long := strings.Repeat(word, maxBodyText/len(word)+10)
	// One word with no space to cut at, made of 3-byte characters so the
	// cap falls inside one.
	unbroken := strings.Repeat("€", maxBodyText)
	exact := strings.Repeat("a", maxBodyText)
	// A word that ends exactly at the cap, with more after it.
	edge := strings.Repeat("y", maxBodyText-len(" ending")) + " ending" + " more"

	cut := BodyText([]TextPart{{FormatPlain, long}})
	split := BodyText([]TextPart{{FormatPlain, unbroken}})
	whole := BodyText([]TextPart{{FormatPlain, exact}})
	kept := BodyText([]TextPart{{FormatPlain, edge}})

	assert.LessOrEqual(t, len(cut), maxBodyText)
	assert.True(t, strings.HasSuffix(cut, " invoices"), "ends on a whole word")
	assert.Greater(t, len(cut), maxBodyText-len(word))
	assert.LessOrEqual(t, len(split), maxBodyText)
	assert.True(t, utf8.ValidString(split))
	assert.Greater(t, len(split), maxBodyText-utf8.UTFMax)
	assert.Equal(t, exact, whole)
	assert.True(t, strings.HasSuffix(kept, " ending"), "keeps a word that ends at the cap")
	assert.Len(t, kept, maxBodyText)
}

// Bytes that are not UTF-8 become spaces rather than reaching the index.
func TestBodyTextInvalidUTF8(t *testing.T) {
	got := BodyText([]TextPart{{FormatPlain, "caf\xe9 au lait"}})

	assert.Equal(t, "caf au lait", got)
}
