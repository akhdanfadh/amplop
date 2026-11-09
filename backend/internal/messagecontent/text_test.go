package messagecontent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeText(t *testing.T) {
	tests := []struct {
		name    string
		part    Part
		content string
		want    string
	}{
		{"utf-8", Part{MediaType: mediaPlain, Charset: "utf-8"}, "caf\xc3\xa9", "café"},
		{"latin-1", Part{MediaType: mediaPlain, Charset: "iso-8859-1"}, "caf\xe9", "café"},
		{"us-ascii reads as windows-1252", Part{MediaType: mediaPlain, Charset: "us-ascii"}, "\x93hi\x94", "\u201chi\u201d"},
		{"windows-1251", Part{MediaType: mediaPlain, Charset: "windows-1251"}, "\xcf\xf0\xe8\xe2\xe5\xf2", "Привет"},
		{"shift_jis", Part{MediaType: mediaPlain, Charset: "shift_jis"}, "\x82\xb1\x82\xf1\x82\xc9\x82\xbf\x82\xcd", "こんにちは"},
		{"gb2312", Part{MediaType: mediaPlain, Charset: "gb2312"}, "\xc4\xe3\xba\xc3", "你好"},
		{"alias", Part{MediaType: mediaPlain, Charset: "utf8"}, "caf\xc3\xa9", "café"},
		{"no charset, utf-8", Part{MediaType: mediaPlain}, "caf\xc3\xa9\r\n", "café\r\n"},
		{"no charset, not utf-8", Part{MediaType: mediaPlain}, "caf\xe9", "café"},
		{"unknown charset, utf-8", Part{MediaType: mediaPlain, Charset: "x-unknown"}, "caf\xc3\xa9\r\n", "café\r\n"},
		{"unknown charset, not utf-8", Part{MediaType: mediaPlain, Charset: "x-unknown"}, "caf\xe9", "café"},
		{"invalid utf-8", Part{MediaType: mediaPlain, Charset: "utf-8"}, "caf\xe9!", "caf\uFFFD!"},
		{"byte order mark", Part{MediaType: mediaPlain, Charset: "utf-8"}, "\xef\xbb\xbfhi", "hi"},
		{"unsafe charset", Part{MediaType: mediaPlain, Charset: "iso-2022-kr"}, "\x1b$)Chi", "\uFFFD"},
		{"empty", Part{MediaType: mediaPlain, Charset: "utf-8"}, "", ""},
		{"html charset", Part{MediaType: mediaHTML, Charset: "iso-8859-1"}, "<p>caf\xe9</p>", "<p>café</p>"},
		{"html charset wins over meta", Part{MediaType: mediaHTML, Charset: "utf-8"}, `<meta charset="windows-1251"><p>caf` + "\xc3\xa9</p>", `<meta charset="windows-1251"><p>café</p>`},
		{"html meta", Part{MediaType: mediaHTML}, `<meta charset="windows-1251"><p>` + "\xcf\xf0\xe8\xe2\xe5\xf2", `<meta charset="windows-1251"><p>Привет`},
		{"html meta http-equiv", Part{MediaType: mediaHTML}, `<meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1">caf` + "\xe9", `<meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1">café`},
		{"html unknown charset, meta", Part{MediaType: mediaHTML, Charset: "x-unknown"}, `<meta charset="iso-8859-1">caf` + "\xe9", `<meta charset="iso-8859-1">café`},
		{"html no charset, utf-8", Part{MediaType: mediaHTML}, "<p>caf\xc3\xa9</p>", "<p>café</p>"},
		{"html no charset, not utf-8", Part{MediaType: mediaHTML}, "<p>caf\xe9</p>", "<p>café</p>"},
		{"html utf-16 byte order mark", Part{MediaType: mediaHTML}, "\xff\xfeh\x00i\x00", "hi"},
		{"windows-1252 label on ascii", Part{MediaType: mediaPlain, Charset: "windows-1252"}, "cafe", "cafe"},
		{"utf-16 label, big-endian mark", Part{MediaType: mediaPlain, Charset: "utf-16"}, "\xfe\xff\x00h\x00i", "hi"},
		{"plain utf-16 mark over latin-1 label", Part{MediaType: mediaPlain, Charset: "iso-8859-1"}, "\xff\xfeh\x00i\x00", "hi"},
		{"html meta utf-8, stray byte", Part{MediaType: mediaHTML}, `<meta charset="utf-8"><p>caf` + "\xc3\xa9 \xff</p>", `<meta charset="utf-8"><p>café ` + "\uFFFD</p>"},
		{"html meta utf-16 reads as utf-8", Part{MediaType: mediaHTML}, `<meta charset="utf-16"><p>caf` + "\xc3\xa9</p>", `<meta charset="utf-16"><p>café</p>`},
		{"html meta unknown", Part{MediaType: mediaHTML}, `<meta charset="x-nothing"><p>caf` + "\xe9</p>", `<meta charset="x-nothing"><p>café</p>`},
		{"html meta in comment", Part{MediaType: mediaHTML}, `<!-- <meta charset="windows-1251"> --><p>caf` + "\xc3\xa9</p>", `<!-- <meta charset="windows-1251"> --><p>café</p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, string(DecodeText(tt.part, []byte(tt.content))))
		})
	}
}
