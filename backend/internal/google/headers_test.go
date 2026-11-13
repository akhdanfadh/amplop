package google

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"inbok/internal/mailsync"
	"inbok/internal/msgid"
)

func TestDecodeText(t *testing.T) {
	tests := []struct {
		name, value, want string
	}{
		{name: "plain", value: "Lunch on Friday?", want: "Lunch on Friday?"},
		{name: "UTF-8 base64", value: "=?UTF-8?B?w4lsaXNl?=", want: "Élise"},
		{name: "ISO-8859-1 quoted", value: "=?ISO-8859-1?Q?Caf=E9?= open", want: "Café open"},
		// Needs go-message's charsets.
		{name: "Windows-1252", value: "=?windows-1252?Q?=93quoted=94?=", want: "“quoted”"},
		{name: "adjacent words join", value: "=?UTF-8?Q?a?= =?UTF-8?Q?b?=", want: "ab"},
		{name: "unknown charset kept", value: "=?x-unknown?Q?abc?=", want: "=?x-unknown?Q?abc?="},
		// PostgreSQL refuses both, which would stop the account's sync.
		{name: "encoded NUL", value: "=?UTF-8?Q?hello=00world?=", want: "hello\uFFFDworld"},
		{name: "encoded invalid UTF-8", value: "=?UTF-8?B?/w==?= x", want: "\uFFFD x"},
		{name: "raw NUL", value: "a\x00b", want: "a\uFFFDb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decodeText(tc.value))
		})
	}
}

func TestParseAddresses(t *testing.T) {
	tests := []struct {
		name, value string
		want        []mailsync.Address
	}{
		{name: "empty", value: " ", want: nil},
		{name: "bare", value: "blair@example.com", want: []mailsync.Address{{Address: "blair@example.com"}}},
		{
			name:  "named",
			value: `Blair Chen <blair@example.com>, "Doe, Jo" <jo@example.com>`,
			want:  []mailsync.Address{{Name: "Blair Chen", Address: "blair@example.com"}, {Name: "Doe, Jo", Address: "jo@example.com"}},
		},
		{name: "encoded name", value: "=?UTF-8?B?w4lsaXNl?= <e@example.com>", want: []mailsync.Address{{Name: "Élise", Address: "e@example.com"}}},
		{
			name:  "group",
			value: "Team: a@example.com, b@example.com;, c@example.com",
			want:  []mailsync.Address{{Address: "a@example.com"}, {Address: "b@example.com"}, {Address: "c@example.com"}},
		},
		{name: "empty group", value: "undisclosed-recipients:;", want: nil},
		{
			name:  "one bad entry",
			value: `a@example.com, not an address, "Doe, Jo" <jo@example.com>`,
			want: []mailsync.Address{
				{Address: "a@example.com"},
				{Name: "not an address"},
				{Name: "Doe, Jo", Address: "jo@example.com"},
			},
		},
		{name: "bad entry decoded", value: "=?UTF-8?B?w4lsaXNl?=, a@example.com", want: []mailsync.Address{{Name: "Élise"}, {Address: "a@example.com"}}},
		{
			name:  "group beside a bad entry",
			value: "broken, Team: a@example.com, b@example.com;, c@example.com",
			want: []mailsync.Address{
				{Name: "broken"},
				{Address: "a@example.com"},
				{Address: "b@example.com"},
				{Address: "c@example.com"},
			},
		},
		{
			name:  "colon that opens no group",
			value: "Re: Blair <blair@example.com>, c@example.com",
			want:  []mailsync.Address{{Name: "Re: Blair <blair@example.com>"}, {Address: "c@example.com"}},
		},
		// The breakages enmime's parser mends.
		{name: "encoded word in quotes", value: `"=?UTF-8?B?w4lsaXNl?=" <e@example.com>`, want: []mailsync.Address{{Name: "Élise", Address: "e@example.com"}}},
		{
			name:  "missing comma",
			value: "Blair <blair@example.com> casey@example.com",
			want:  []mailsync.Address{{Name: "Blair", Address: "blair@example.com"}, {Address: "casey@example.com"}},
		},
		{name: "folded inside brackets", value: "Blair <blair@\r\n example.com>", want: []mailsync.Address{{Name: "Blair", Address: "blair@example.com"}}},
		{
			name:  "comment after an address",
			value: "Blair <blair@example.com> (work), <bad",
			want:  []mailsync.Address{{Name: "Blair", Address: "blair@example.com"}, {Name: "<bad"}},
		},
		{
			name:  "missing comma beside a bad entry",
			value: "Blair <blair@example.com> casey@example.com, not an address",
			want: []mailsync.Address{
				{Name: "Blair", Address: "blair@example.com"},
				{Address: "casey@example.com"},
				{Name: "not an address"},
			},
		},
		// RFC 2047 section 5 lets text that looks like an encoded word be
		// encoded itself; it decodes once, to that text.
		{
			name:  "encoded encoded word",
			value: "=?UTF-8?B?PT9VVEYtOD9RP0FsaWNlPz0=?= <attacker@example.com>",
			want:  []mailsync.Address{{Name: "=?UTF-8?Q?Alice?=", Address: "attacker@example.com"}},
		},
		{
			name:  "quoted text beside an encoded word",
			value: `"Dr. =?UTF-8?Q?x?=" <a@example.com>`,
			want:  []mailsync.Address{{Name: "Dr. =?UTF-8?Q?x?=", Address: "a@example.com"}},
		},
		{
			name:  "quoted and unquoted encoded names",
			value: `"=?UTF-8?B?w4lsaXNl?=" <e@example.com>, =?UTF-8?Q?Caf=C3=A9?= <c@example.com>`,
			want:  []mailsync.Address{{Name: "Élise", Address: "e@example.com"}, {Name: "Café", Address: "c@example.com"}},
		},
		{
			name:  "missing comma after a comment",
			value: "A <a@example.com> (work) B <b@example.com>",
			want:  []mailsync.Address{{Name: "A", Address: "a@example.com"}, {Name: "B", Address: "b@example.com"}},
		},
		{
			name:  "missing comma after two comments",
			value: `A <a@example.com> (one) (tw\) o) B <b@example.com>`,
			want:  []mailsync.Address{{Name: "A", Address: "a@example.com"}, {Name: "B", Address: "b@example.com"}},
		},
		{
			name:  "bad member inside a group",
			value: "Team: a@example.com, broken, b@example.com;, c@example.com",
			want: []mailsync.Address{
				{Address: "a@example.com"},
				{Name: "broken"},
				{Address: "b@example.com"},
				{Address: "c@example.com"},
			},
		},
		{
			name:  "bad first and last group members",
			value: "Team: broken, a@example.com, also broken;",
			want:  []mailsync.Address{{Name: "broken"}, {Address: "a@example.com"}, {Name: "also broken"}},
		},
		{name: "encoded NUL in a name", value: "=?UTF-8?Q?a=00b?= <a@example.com>", want: []mailsync.Address{{Name: "a\uFFFDb", Address: "a@example.com"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseAddresses(tc.value))
		})
	}
}

func TestParseAddressesLimit(t *testing.T) {
	entries := make([]string, maxAddresses+10)
	for i := range entries {
		entries[i] = "r" + strconv.Itoa(i) + "@example.com"
	}

	addresses := parseAddresses(strings.Join(entries, ", "))

	require.Len(t, addresses, maxAddresses)
	assert.Equal(t, "r0@example.com", addresses[0].Address)
}

func TestSplitAddresses(t *testing.T) {
	value := `"a, \"b\"" <a@x>, (c, d) e@x, <f,g@x>, , h@x`

	assert.Equal(t, []string{`"a, \"b\"" <a@x>`, "(c, d) e@x", "<f,g@x>", "h@x"}, splitAddresses(value))
}

func TestParseMsgIDs(t *testing.T) {
	tests := []struct {
		name, value string
		want        []string
	}{
		{name: "none", value: "", want: nil},
		{name: "one", value: "<lunch-1@example.com>", want: []string{"<lunch-1@example.com>"}},
		{name: "folded list", value: "<a@x>\r\n <b@x>\t<c@x>", want: []string{"<a@x>", "<b@x>", "<c@x>"}},
		// RFC 5322 section 4.5.4's obsolete phrases, as old mailers write.
		{name: "phrase", value: `Your message of "Monday" <a@x>`, want: []string{"<a@x>"}},
		{name: "comment skipped", value: "(see <old@x>) <a@x>", want: []string{"<a@x>"}},
		{name: "quoted phrase skipped", value: `"see <fake@x>" <real@x>`, want: []string{"<real@x>"}},
		{name: "escaped quote in a phrase", value: `"a \" <fake@x>" <real@x>`, want: []string{"<real@x>"}},
		// RFC 5322 section 3.2.2's quoted-pairs in a comment.
		{name: "escaped paren in a comment", value: `(note \) <fake@x>) <real@x>`, want: []string{"<real@x>"}},
		{name: "escaped open paren", value: `(note \( still one) <real@x>`, want: []string{"<real@x>"}},
		{name: "NUL inside", value: "<a\x00b@x>", want: []string{"<a\uFFFDb@x>"}},
		// mox's ReferencedIDs mends a msg-id folded inside.
		{name: "folded inside", value: "<abc@exam\r\n ple.com> <d@x>", want: []string{"<abc@example.com>", "<d@x>"}},
		{name: "cut off", value: "<a@x <b@x>", want: []string{"<b@x>"}},
		// RFC 5322's quoted local part (obsolete in a msg-id) and domain
		// literal may hold the brackets.
		{name: "quoted local part", value: `<"a>b"@x> <real@x>`, want: []string{`<"a>b"@x>`, "<real@x>"}},
		{name: "domain literal", value: "<a@[b>c]> <real@x>", want: []string{"<a@[b>c]>", "<real@x>"}},
		{name: "unclosed quote inside", value: `<"abc@x> <real@x>`, want: []string{`<"abc@x>`, "<real@x>"}},
		{name: "empty brackets", value: "<> <a@x>", want: []string{"<a@x>"}},
		{name: "unterminated", value: "<a@x> <b@x", want: []string{"<a@x>"}},
		{name: "no at sign", value: "<local-only>", want: []string{"<local-only>"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseMsgIDs(tc.value))
		})
	}
}

// A References header past the byte limit keeps its first msg-id and its
// last ones, the nearest parents.
func TestParseReferencesLong(t *testing.T) {
	ids := make([]string, 700)
	for i := range ids {
		ids[i] = fmt.Sprintf("<long-message-id-%d@example.com>", i)
	}
	value := strings.Join(ids, " ")
	require.Greater(t, len(value), maxHeaderBytes)

	references := parseReferences(value)

	require.Len(t, references, msgid.MaxReferences)
	assert.Equal(t, ids[0], references[0])
	assert.Equal(t, ids[len(ids)-msgid.MaxReferences+1:], references[1:])
}

// A cut never splits a character and never keeps the whole value.
func TestCut(t *testing.T) {
	long := strings.Repeat("a", maxHeaderBytes-1) + "\u00e9" + strings.Repeat("b", 1<<20)

	got := cut(long)

	assert.True(t, utf8.ValidString(got))
	assert.Len(t, got, maxHeaderBytes-1)
	assert.NotSame(t, unsafe.StringData(long), unsafe.StringData(got))
	assert.Equal(t, "short", cut("short"))
}

// A field is found in any case, the first of a repeated one wins, and a
// long one is cut before anything parses it.
func TestHeadersGet(t *testing.T) {
	long := strings.Repeat("a", maxHeaderBytes+100)
	h := headers{
		nil,
		{Name: "message-id", Value: "<a@x>"},
		{Name: "Message-ID", Value: "<b@x>"},
		{Name: "Subject", Value: long},
	}

	assert.Equal(t, "<a@x>", h.get(headerMessageID))
	assert.Len(t, h.get(headerSubject), maxHeaderBytes)
	assert.Len(t, h.full(headerSubject), len(long))
	assert.Empty(t, h.get(headerCc))
}
