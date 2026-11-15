package google

import (
	"net/url"

	"inbok/internal/connectedaccount"
	"inbok/internal/conversation"
)

// WebApp finds conversations in Gmail's own web app.
type WebApp struct{}

// ConversationURL returns where Gmail's web app shows c, a conversation of
// source's account: https://mail.google.com/mail/?authuser=<address>#all/<thread>,
// or #spam/ or #trash/ in place of #all/ for a conversation only there. It
// returns empty for an account of another provider.
//
// NOTE: Gmail documents no such address, so it may stop working without
// notice. authuser picks which of the browser's signed-in Google accounts
// opens it, and the fragment names a view and the thread by its Gmail API
// ID (ADR031, "Mail actions as built", checkpoint 7a).
func (WebApp) ConversationURL(source conversation.Source, c conversation.Conversation) string {
	if source.Provider != connectedaccount.ProviderGmail {
		return ""
	}
	// All mail leaves out Spam and Trash, so a conversation only there
	// opens in its own view.
	view := "all"
	switch {
	case c.InAllMail:
	case c.InSpam:
		view = "spam"
	case c.InTrash:
		view = "trash"
	}
	link := url.URL{
		Scheme:   "https",
		Host:     "mail.google.com",
		Path:     "/mail/",
		RawQuery: url.Values{"authuser": {source.Address}}.Encode(),
		Fragment: view + "/" + source.ThreadID,
	}
	return link.String()
}
