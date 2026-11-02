package account

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckPassword(t *testing.T) {
	for _, c := range []struct {
		name, password string
		valid          bool
	}{
		{"empty", "", false},
		{"too short", strings.Repeat("a", 14), false},
		{"minimum", strings.Repeat("a", 15), true},
		{"maximum", strings.Repeat("a", 128), true},
		{"too long", strings.Repeat("a", 129), false},
		{"Unicode too short", strings.Repeat("界", 14), false},
		{"Unicode minimum", strings.Repeat("界", 15), true},
		{"Unicode maximum", strings.Repeat("界", 128), true},
		{"Unicode too long", strings.Repeat("界", 129), false},
		{"spaces preserved", "  a long password  ", true},
		{"invalid UTF-8", strings.Repeat("a", 15) + "\xff", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := CheckPassword(c.password)

			if !c.valid {
				require.ErrorIs(t, err, ErrPasswordInvalid)
				return
			}
			require.NoError(t, err)
		})
	}
}
