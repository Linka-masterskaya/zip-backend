package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectMIMEAcceptsGIF(t *testing.T) {
	data := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00")
	assert.Equal(t, "image/gif", detectMIME(data))
}
