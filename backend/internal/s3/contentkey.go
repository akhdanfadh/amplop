package s3

import (
	"strconv"
	"strings"

	"uuid"

	"inbok/internal/messagecontent"
)

// accountsPrefix starts every content key: accounts/<account>/<content ID>
// (ADR009).
const accountsPrefix = "accounts/"

// contentKey returns the object key of ref.
func contentKey(ref messagecontent.Ref) string {
	return accountPrefix(ref.Account) + ref.ID.String()
}

// accountPrefix returns the key prefix of account's content, slash
// included, so account 1's prefix does not also match account 11.
func accountPrefix(account int64) string {
	return accountsPrefix + strconv.FormatInt(account, 10) + "/"
}

// parseContentKey returns the ref whose [contentKey] is key, and false
// when no ref has it.
func parseContentKey(key string) (messagecontent.Ref, bool) {
	rest, ok := strings.CutPrefix(key, accountsPrefix)
	if !ok {
		return messagecontent.Ref{}, false
	}
	accountText, idText, ok := strings.Cut(rest, "/")
	if !ok {
		return messagecontent.Ref{}, false
	}
	account, err := strconv.ParseInt(accountText, 10, 64)
	if err != nil {
		return messagecontent.Ref{}, false
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		return messagecontent.Ref{}, false
	}
	ref := messagecontent.Ref{Account: account, ID: id}
	// Other spellings, e.g. 01 or an upper-case ID, parse to a ref whose
	// key differs, so deleting that ref would leave this object.
	if contentKey(ref) != key {
		return messagecontent.Ref{}, false
	}
	return ref, true
}
