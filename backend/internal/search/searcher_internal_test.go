package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Spans that leave the text, are empty, or start or end inside a
// character are dropped; the rest are kept in order.
func TestWithin(t *testing.T) {
	text := "é invoice" // é takes two bytes

	got := within(text, []Span{
		{Start: 0, Length: 2},  // é
		{Start: 1, Length: 2},  // starts inside é
		{Start: 0, Length: 1},  // ends inside é
		{Start: 3, Length: 7},  // invoice
		{Start: 3, Length: 8},  // past the end
		{Start: -1, Length: 2}, // before the start
		{Start: 3, Length: 0},  // empty
	})

	assert.Equal(t, []Span{{Start: 0, Length: 2}, {Start: 3, Length: 7}}, got)
}

// Spans come back sorted, overlapping ones merged into one, and adjacent
// ones kept apart.
func TestWithinMerges(t *testing.T) {
	text := "東京都 and more"

	got := within(text, []Span{
		{Start: 13, Length: 4}, // more, out of order
		{Start: 0, Length: 9},  // 東京都
		{Start: 3, Length: 6},  // 京都, inside the one before
		{Start: 9, Length: 1},  // the space, right after it
	})
	partly := within(text, []Span{{Start: 0, Length: 6}, {Start: 3, Length: 6}}) // 東京 and 京都

	assert.Equal(t, []Span{{Start: 0, Length: 9}, {Start: 9, Length: 1}, {Start: 13, Length: 4}}, got)
	assert.Equal(t, []Span{{Start: 0, Length: 9}}, partly)
}
