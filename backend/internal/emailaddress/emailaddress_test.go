package emailaddress

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"lowercase", "admin@example.com", "admin@example.com"},
		{"mixed case", "Mixed.Case+Tag@Example.COM", "mixed.case+tag@example.com"},
		{"outer spaces and tabs", " \tAdmin@EXAMPLE.COM\t ", "admin@example.com"},
		{"non-ASCII kept as typed", "ÉLise@Bücher.DE", "Élise@bücher.de"},
		{"address literal", "Admin@[127.0.0.1]", "admin@[127.0.0.1]"},
		{"shortest", "a@b", "a@b"},
		{"longest", strings.Repeat("a", 64) + "@" + strings.Repeat("b", 189), strings.Repeat("a", 64) + "@" + strings.Repeat("b", 189)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.input)

			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []struct {
		name, input string
	}{
		{"empty", ""},
		{"spaces only", "  "},
		{"no at sign", "admin.example.com"},
		{"display name", "Admin <admin@example.com>"},
		{"angle brackets", "<admin@example.com>"},
		{"quoted local part", `"admin"@example.com`},
		{"list", "a@example.com, b@example.com"},
		{"inner line break", "admin@exam\nple.com"},
		{"outer line break", "admin@example.com\r\n"},
		{"outer no-break space", "\u00a0admin@example.com"},
		{"too long", strings.Repeat("a", 64) + "@" + strings.Repeat("b", 190)},
		{"C1 control", "admin\u0085@example.com"},
		{"zero-width space", "admin\u200b@example.com"},
		{"zero-width space in domain", "admin@exa\u200bmple.com"},
		{"right-to-left override", "admin\u202e@example.com"},
		{"byte order mark", "\ufeffadmin@example.com"},
		{"soft hyphen", "ad\u00admin@example.com"},
		{"inner no-break space", "ad\u00a0min@example.com"},
		{"ideographic space", "admin@ex\u3000ample.com"},
		{"line separator", "admin\u2028@example.com"},
		{"paragraph separator", "admin\u2029@example.com"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.input)

			// The bare error carries no part of the rejected input.
			assert.Equal(t, ErrInvalid, err)
			assert.Empty(t, got)
		})
	}
}

func TestParseIdempotent(t *testing.T) {
	first, err := Parse(" Admin@Example.COM ")
	require.NoError(t, err)

	second, err := Parse(first)

	require.NoError(t, err)
	assert.Equal(t, first, second)
}
