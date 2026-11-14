package search

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxBodyText is the most bytes of body text a document holds.
//
// DESIGN: Meilisearch indexes at most 65,535 word positions of a field and
// drops the rest without a word; a word takes one, a hard separator such
// as a comma or a full stop eight. 64 KiB of prose is about 10,000 words
// and 2,000 such separators, near 26,000 positions, so for prose this cap,
// not Meilisearch, decides what is left out, the same way every time.
// Text dense with separators, e.g. a table pasted as CSV, can still pass
// the limit within the cap, and Meilisearch then drops its end; counting
// positions here would mean copying Meilisearch's tokenizer. The cap also
// keeps a batch of 100 documents, each with two body fields, under about
// 13 MB, far below Meilisearch's 100 MB request limit. Few messages have this much text;
// past it, a newsletter or a long thread is found by its start.
const maxBodyText = 64 << 10

// TextFormat is the media type of a text part.
type TextFormat int

const (
	FormatPlain TextFormat = iota + 1 // text/plain
	FormatHTML                        // text/html
)

// TextPart is one text part of a message, its content decoded to UTF-8 as
// content storage keeps it (ADR009).
type TextPart struct {
	Format  TextFormat
	Content string
}

// maxInput is the most bytes of a message's text parts that [BodyText]
// reads, over all those parts.
//
// DESIGN: reading costs time in proportion to the bytes read, and a message
// comes from any sender; text that prints nothing, e.g. megabytes of
// whitespace, adds no text to stop at. Gmail clips a message whose HTML
// passes 102 KB, so senders of bulk mail keep under it, and 1 MiB is about
// ten times that: far more than [maxBodyText] of text in any ordinary
// message. What it misses is text after a megabyte of markup, e.g. images
// embedded as data URIs; such a message is still found by its headers.
const maxInput = 1 << 20

// maxParts is the most text parts that [BodyText] reads from one message. A message a person wrote has a few; past this many, the
// message is built to cost work, and the first ones hold its text.
const maxParts = 64

// maxDepth is the most elements open at once that [BodyText] follows in an
// HTML part. Past it, text is still read, but further elements are not
// followed: one that hides hides only the text token after it, which is all
// of a script's or a style's content, but a hidden element there shows the
// rest of its text. The limit
// bounds the work each end tag takes to find the element it closes; HTML
// that leaves out end tags, e.g. </p>, can pass it, but mail hides text
// that deep rarely, if ever.
const maxDepth = 512

// The HTML Standard's rendering section, the default style sheet every
// browser starts from, decides which elements a reader sees and where they
// break a line: https://html.spec.whatwg.org/multipage/rendering.html
var (
	// unrendered elements have display: none, so neither they nor their
	// text show (15.3.1, Hidden elements), and noscript.
	//
	// NOTE: a mail reader runs no scripts, so it shows noscript's content,
	// but golang.org/x/net/html's tokenizer reads that content as raw text,
	// markup included, as for a browser that runs scripts. It is hidden
	// rather than indexed as markup.
	unrendered = elements(atom.Area, atom.Base, atom.Basefont, atom.Datalist,
		atom.Head, atom.Link, atom.Meta, atom.Noembed, atom.Noframes,
		atom.Param, atom.Rp, atom.Script, atom.Style, atom.Template,
		atom.Title, atom.Noscript)

	// replaced elements show what they load or play, not their children
	// (15.4.1 Embedded content), but take space between the words beside
	// them; img and embed are void, so have no children.
	replaced = elements(atom.Audio, atom.Embed, atom.Iframe, atom.Img, atom.Video)

	// blocks have display: block, list-item, or a table part, so the words
	// before, inside, and after them stay apart (15.3.3 Flow content, 15.3.6
	// Sections and headings, 15.3.7 Lists, 15.3.8 Tables, 15.3.12 The
	// fieldset and legend elements, 15.5.5 The details and summary
	// elements); br breaks a line (15.3.4 Phrasing content).
	blocks = elements(atom.Address, atom.Blockquote, atom.Center, atom.Dialog,
		atom.Div, atom.Figure, atom.Figcaption, atom.Footer, atom.Form,
		atom.Header, atom.Hr, atom.Legend, atom.Listing, atom.Main, atom.P,
		atom.Plaintext, atom.Pre, atom.Search, atom.Xmp,
		atom.Article, atom.Aside, atom.H1, atom.H2, atom.H3, atom.H4,
		atom.H5, atom.H6, atom.Hgroup, atom.Nav, atom.Section,
		atom.Dir, atom.Dd, atom.Dl, atom.Dt, atom.Menu, atom.Ol, atom.Ul,
		atom.Li,
		atom.Table, atom.Caption, atom.Colgroup, atom.Col, atom.Thead,
		atom.Tbody, atom.Tfoot, atom.Tr, atom.Td, atom.Th,
		atom.Fieldset, atom.Details, atom.Summary,
		atom.Br)

	// voids never have an end tag or content, so they are never open: the
	// void elements (13.1.2, Elements), and the older ones the parser
	// treats the same way (13.2.6.4.4 and 13.2.6.4.7, the "in head" and
	// "in body" insertion modes).
	voids = elements(atom.Area, atom.Base, atom.Basefont, atom.Bgsound, atom.Br,
		atom.Col, atom.Embed, atom.Frame, atom.Hr, atom.Img, atom.Input,
		atom.Keygen, atom.Link, atom.Meta, atom.Param, atom.Source, atom.Track,
		atom.Wbr)

	// headContent may stand in head; any other start tag, or text that is
	// not whitespace, ends a head left open, as the parser's "in head"
	// rules do (13.2.6.4.4).
	headContent = elements(atom.Base, atom.Basefont, atom.Bgsound, atom.Link,
		atom.Meta, atom.Title, atom.Noscript, atom.Noframes, atom.Style,
		atom.Script, atom.Template)
)

// attrHidden hides an element and its content (15.3.1).
const attrHidden = "hidden"

// elements returns a set of elements, by the atom the tokenizer gives each.
func elements(list ...atom.Atom) map[atom.Atom]bool {
	set := make(map[atom.Atom]bool, len(list))
	for _, element := range list {
		set[element] = true
	}
	return set
}

// BodyText returns the text a search matches in parts, one group of a
// message's text parts, in order: plain text as it is, and the text of
// HTML that a reader sees, as the HTML Standard renders it; a part of no
// known format is skipped. Runs of whitespace become one space,
// characters that print nothing are dropped, parts are separated by a
// space, and the result is cut at a word to at most 64 KiB. It reads at
// most 64 parts and 1 MiB of them, and only until it has more than
// 64 KiB, enough to cut at a word.
//
// DESIGN: the caller groups the parts, e.g. the ones a view shows and the
// rest, each group becoming a field of its own (ADR005).
func BodyText(parts []TextPart) string {
	text := normalizer{limit: maxBodyText}
	count, budget := 0, maxInput
	for _, part := range parts {
		if text.full() || count == maxParts || budget == 0 {
			break
		}
		var read func(content string) string
		switch part.Format {
		case FormatPlain:
			read = func(content string) string { return content }
		case FormatHTML:
			read = htmlText
		default:
			continue
		}
		count++
		// A cut can split a character, which the normalizer reads as a
		// space, or a tag or a character reference: the tokenizer drops an
		// unfinished tag, and what is left is a stray < or &.
		content := part.Content[:min(len(part.Content), budget)]
		budget -= len(content)
		text.add(read(content))
	}
	// A copy, so the result keeps none of the builder's spare capacity.
	return strings.Clone(cut(text.out.String(), maxBodyText))
}

// htmlText returns the text of an HTML document that a reader sees, with
// entities decoded: no text of an element the HTML Standard does not
// render or that has the hidden attribute, and a space around every block
// and replaced element. It reads the document once, token by token,
// following at most [maxDepth] open elements.
//
// DESIGN: it reads tokens rather than building the document's tree with
// html.Parse. The parser's error recovery, which repairs misnested markup
// as a browser does, lets a crafted document of a few kilobytes take
// hundreds of megabytes or seconds of CPU in golang.org/x/net v0.62.0, and
// a sender writes the HTML. HTML-to-text libraries either build the same
// tree or run table cells' words together; ADR005 lists each one
// considered and why it was not used. A stack of open elements is all that finding
// where a hidden element ends needs. Without the parser's repairs, an end
// tag closes the nearest open element of its name and is ignored when none
// is open, and an unclosed head is closed as the parser closes it. That
// mostly moves a space; but a hidden element whose end tag HTML lets an
// author leave out, e.g. p, li, dd, dt, tr, td, th, option, or rp, hides
// the text after it until an enclosing element ends. Mail hides text with
// style, not hidden, so this is accepted.
func htmlText(content string) string {
	tokens := html.NewTokenizer(strings.NewReader(content))
	var text strings.Builder
	var open []atom.Atom
	// hiding is the index in open of the element whose content is hidden,
	// or -1 when text shows.
	hiding := -1
	// skip hides the next text token, for an element past maxDepth.
	skip := false
	for {
		kind := tokens.Next()
		switch kind {
		case html.ErrorToken:
			// The end of the content, or of what the tokenizer can read.
			return text.String()
		case html.TextToken:
			if skip {
				skip = false
				continue
			}
			data := tokens.Text()
			if inHead(open, hiding) && len(bytes.TrimSpace(data)) > 0 {
				open, hiding = open[:hiding], -1
			}
			if hiding < 0 {
				text.Write(data)
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokens.TagName()
			element := atom.Lookup(name)
			hidden := hasAttr && hasHidden(tokens)
			if inHead(open, hiding) && !headContent[element] {
				open, hiding = open[:hiding], -1
			}
			if hiding < 0 && !hidden && (blocks[element] || replaced[element]) {
				text.WriteByte(' ')
			}
			// A self-closing tag on a non-void element starts it, as the
			// parser reads it (13.2.6.4.7): the tokenizer reads what follows
			// <script/> as the script's raw text too.
			if voids[element] {
				continue
			}
			hides := hidden || unrendered[element] || replaced[element]
			// Past maxDepth an element is not followed, but one that hides
			// still hides the text token after it: all of a raw-text
			// element's content, e.g. a script's or a style's.
			if len(open) == maxDepth {
				skip = hiding < 0 && hides
				continue
			}
			if hiding < 0 && hides {
				hiding = len(open)
			}
			open = append(open, element)
		case html.EndTagToken:
			name, _ := tokens.TagName()
			element := atom.Lookup(name)
			at := lastIndex(open, element)
			if at < 0 {
				continue
			}
			// A hidden element takes no space.
			ended := at <= hiding
			if ended {
				hiding = -1
			}
			open = open[:at]
			if hiding < 0 && !ended && blocks[element] {
				text.WriteByte(' ')
			}
		}
	}
}

// inHead reports whether head is the innermost open element and the one
// hiding, i.e. whether the next token stands directly in an open head.
func inHead(open []atom.Atom, hiding int) bool {
	return hiding >= 0 && hiding == len(open)-1 && open[hiding] == atom.Head
}

// hasHidden reports whether the tag tokens is at has the hidden attribute.
// It reads the tag's attributes.
func hasHidden(tokens *html.Tokenizer) bool {
	for {
		key, _, more := tokens.TagAttr()
		if string(key) == attrHidden {
			return true
		}
		if !more {
			return false
		}
	}
}

// lastIndex returns the index of the last element in open, or -1.
func lastIndex(open []atom.Atom, element atom.Atom) int {
	for n := len(open) - 1; n >= 0; n-- {
		if open[n] == element {
			return n
		}
	}
	return -1
}

// zeroWidthSpace, U+200B, marks where one word ends and the next begins
// without a visible space (The Unicode Standard, section 23.2), unlike the
// other format characters, e.g. a soft hyphen, which sit inside a word.
const zeroWidthSpace = '\u200b'

// normalizer collects text as valid UTF-8, with each run of whitespace,
// and each start of another text, as one space, no space at either end,
// and no characters that print nothing. It stops once it holds more than
// limit bytes.
type normalizer struct {
	out   strings.Builder
	limit int
	space bool // a space goes before the next character written
}

// add appends text, until the normalizer is full.
func (n *normalizer) add(text string) {
	n.space = n.out.Len() > 0
	for _, r := range text {
		if n.full() {
			return
		}
		switch {
		// A byte that is not UTF-8 reads as utf8.RuneError.
		case unicode.IsSpace(r), r == zeroWidthSpace, r == utf8.RuneError:
			n.space = true
		// Controls, e.g. NUL or ESC; format characters, e.g. soft hyphens
		// and zero-width joiners; and other characters Unicode says to
		// show as nothing, e.g. the combining grapheme joiner and the
		// variation selectors. Newsletters pad their preview text with
		// several of them.
		case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector):
		default:
			if n.space && n.out.Len() > 0 {
				n.out.WriteByte(' ')
			}
			n.space = false
			n.out.WriteRune(r)
		}
	}
}

// full reports whether the normalizer holds more than its limit.
func (n *normalizer) full() bool {
	return n.out.Len() > n.limit
}

// cut returns text, as a [normalizer] writes it, at most limit bytes long:
// up to the last space within the limit, or, for one word longer than
// that, up to the last whole character.
func cut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	if text[end] == ' ' {
		return text[:end]
	}
	if space := strings.LastIndexByte(text[:end], ' '); space > 0 {
		return text[:space]
	}
	return text[:end]
}
