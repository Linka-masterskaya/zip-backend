package picturebank

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeLikePatternTreatsUserQueryLiterally(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "ordinary", input: "кот", expected: "кот"},
		{name: "percent", input: "100%", expected: `100\%`},
		{name: "underscore", input: "a_b", expected: `a\_b`},
		{name: "backslash", input: `a\b`, expected: `a\\b`},
		{name: "mixed wildcards", input: `50%_off\today`, expected: `50\%\_off\\today`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, escapeLikePattern(test.input))
		})
	}
}
