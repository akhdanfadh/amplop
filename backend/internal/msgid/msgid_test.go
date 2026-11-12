package msgid

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWritable(t *testing.T) {
	// 250 octets with its brackets, the most RFC 5536 allows.
	longest := "<" + strings.Repeat("a", 240) + "@example>"
	require.Len(t, longest, 250)

	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "message id", id: "<abc.123@mail.example.com>", want: true},
		{name: "atext", id: "<a+b=c_d!#$%&'*/?^`{|}~-e@example.com>", want: true},
		{name: "domain literal", id: "<a@[127.0.0.1]>", want: true},
		{name: "longest", id: longest, want: true},
		{name: "empty", id: "", want: false},
		{name: "empty brackets", id: "<>", want: false},
		{name: "no brackets", id: "abc@example.com", want: false},
		{name: "no closing bracket", id: "<abc@example.com", want: false},
		{name: "too long", id: "<a" + longest[1:], want: false},
		// Invalid in RFC 5322's every syntax, or, for the quoted local part,
		// the obsolete syntax section 4 forbids generating.
		{name: "no at sign", id: "<local-only>", want: false},
		{name: "two at signs", id: "<a@b@example.com>", want: false},
		{name: "empty left", id: "<@example.com>", want: false},
		{name: "empty right", id: "<a@>", want: false},
		{name: "quoted local part", id: `<"a.b"@example.com>`, want: false},
		{name: "empty dot-atom part", id: "<a..b@example.com>", want: false},
		{name: "literal on the left", id: "<[a]@example.com>", want: false},
		{name: "unclosed literal", id: "<a@[127.0.0.1>", want: false},
		{name: "bracket in literal", id: "<a@[1[2]>", want: false},
		{name: "backslash in literal", id: `<a@[1\2]>`, want: false},
		{name: "space", id: "<a b@example.com>", want: false},
		{name: "space in literal", id: "<a@[1 2]>", want: false},
		{name: "two ids", id: "<a@example.com> <b@example.com>", want: false},
		{name: "two ids without space", id: "<a@example.com><b@example.com>", want: false},
		{name: "line break", id: "<a@example.com\r\nBcc: x@example.com>", want: false},
		{name: "control character", id: "<a\x01@example.com>", want: false},
		{name: "delete", id: "<a\x7f@example.com>", want: false},
		{name: "non-ASCII", id: "<caf\u00e9@example.com>", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Writable(tc.id))
		})
	}
}

func TestTrimReferences(t *testing.T) {
	ids := make([]string, MaxReferences+5)
	for i := range ids {
		ids[i] = "<" + strconv.Itoa(i) + "@x>"
	}

	trimmed := TrimReferences(ids)

	require.Len(t, trimmed, MaxReferences)
	assert.Equal(t, ids[0], trimmed[0], "the thread's first message stays")
	assert.Equal(t, ids[len(ids)-MaxReferences+1:], trimmed[1:], "the nearest parents stay")
	assert.Equal(t, ids[:3], TrimReferences(ids[:3]))
}
