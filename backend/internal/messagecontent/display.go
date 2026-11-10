package messagecontent

import "slices"

// Display is what a message view shows of a message.
type Display struct {
	Bodies      []Part // text parts to show, one after another
	Attachments []Part // parts to offer as downloads, in message order
	// Inline is the inline parts that HTML in [Display.Bodies] can show by
	// Content-ID, in message order; see [Display.Downloads].
	Inline []Part
}

// Choose returns what a view shows of the message whose part tree is
// root: in multipart/alternative, the last alternative that has HTML,
// else the last one that has text; in multipart/related, the part its
// start parameter names, else its first, whose inline parts HTML shows
// by Content-ID, while every part under its other parts is offered as a
// download; in any other multipart, every part in order. An inline part
// that no HTML of its multipart/related can show is offered as a
// download.
//
// DESIGN: It runs when a message is shown, on what PostgreSQL keeps, so
// a better choice reaches stored mail without fetching it again (ADR009).
// Alternatives not chosen are the same message in another form (RFC 2046,
// section 5.1.4), so their parts are left out, attachments included.
func Choose(root Part) Display {
	var d Display
	d.add(root)
	return d
}

// add adds what a view shows of part to d.
func (d *Display) add(part Part) {
	switch part.Kind {
	case KindText:
		d.Bodies = append(d.Bodies, part)
	case KindMultipart:
		d.addMultipart(part)
	default:
		// An attachment, an inline part no HTML here shows, or a kind this
		// code does not know, e.g. from a bad row: a download, never lost.
		d.Attachments = append(d.Attachments, part)
	}
}

// addMultipart adds what a view shows of part, a multipart, to d.
func (d *Display) addMultipart(part Part) {
	switch part.MediaType {
	case mediaAlternative:
		d.addAlternative(part.Parts)
	case mediaRelated:
		d.addRelated(part)
	default:
		// An unknown multipart reads as multipart/mixed (RFC 2046, section 5.1.3).
		for _, child := range part.Parts {
			d.add(child)
		}
	}
}

// addAlternative adds the alternative a view shows, of parts, to d.
func (d *Display) addAlternative(parts []Part) {
	if len(parts) == 0 {
		return
	}
	// Alternatives come in increasing faithfulness to the original, so the
	// last one a view can show is best (RFC 2046, section 5.1.4).
	var last, withText, withHTML *Display
	for _, part := range parts {
		// Each part is walked once, so nested alternatives cost no more
		// than the parts they hold.
		shown := Choose(part)
		last = &shown
		if len(shown.Bodies) == 0 {
			continue
		}
		withText = &shown
		if slices.ContainsFunc(shown.Bodies, isHTML) {
			withHTML = &shown
		}
	}
	switch {
	case withHTML != nil:
		d.merge(*withHTML)
	case withText != nil:
		d.merge(*withText)
	default:
		d.merge(*last)
	}
}

// addRelated adds what a view shows of related, a multipart/related, to d.
func (d *Display) addRelated(related Part) {
	if len(related.Parts) == 0 {
		return
	}
	// The part start names is the one shown, else the first; the others
	// are what it refers to (RFC 2387, section 3.2).
	root := slices.IndexFunc(related.Parts, func(part Part) bool {
		return related.Start != "" && part.ContentID == related.Start
	})
	root = max(root, 0)
	shown := Choose(related.Parts[root])
	// Only HTML can show an inline part; plain text would lose it.
	html := slices.ContainsFunc(shown.Bodies, isHTML)
	// Parts are added in message order, wherever the shown part is.
	for i, part := range related.Parts {
		if i == root {
			d.merge(shown)
			continue
		}
		// Any other part is something the shown one refers to (RFC 2387,
		// section 3.2), never a body: its text would show twice, or pick
		// an alternative the shown part would not.
		for _, leaf := range leaves(part) {
			switch {
			// An image its HTML can show by cid:.
			case leaf.Kind == KindInline && html:
				d.Inline = append(d.Inline, leaf)
			// Anything else, text included: a download, never lost.
			default:
				d.Attachments = append(d.Attachments, leaf)
			}
		}
	}
}

// leaves returns part if it holds no parts, else the parts under it
// that hold none, in message order.
func leaves(part Part) []Part {
	if part.Kind != KindMultipart {
		return []Part{part}
	}
	var out []Part
	for _, child := range part.Parts {
		out = append(out, leaves(child)...)
	}
	return out
}

// merge appends shown to d.
func (d *Display) merge(shown Display) {
	d.Bodies = append(d.Bodies, shown.Bodies...)
	d.Attachments = append(d.Attachments, shown.Attachments...)
	d.Inline = append(d.Inline, shown.Inline...)
}

// Downloads returns the parts a view offers as downloads: d's
// attachments, then its inline parts whose IDs are not in shown, the
// [Sanitized.Shown] of its HTML bodies.
func (d Display) Downloads(shown []string) []Part {
	downloads := slices.Clone(d.Attachments)
	// An image the HTML never refers to would otherwise be lost.
	for _, part := range d.Inline {
		if !slices.Contains(shown, part.ID) {
			downloads = append(downloads, part)
		}
	}
	return downloads
}

// isHTML reports whether part is HTML.
func isHTML(part Part) bool {
	return part.MediaType == mediaHTML
}
