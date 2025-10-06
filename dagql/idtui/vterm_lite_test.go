package idtui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursorBuffer(t *testing.T) {
	l := cursorBuffer{}
	l.Write([]byte("hello "))
	l.Write([]byte("world"))
	require.Equal(t, "hello world", l.String())
	l.Write([]byte("!\r'"))
	require.Equal(t, "'ello world!", l.String())
}
