package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUTF8DanglingWriter(t *testing.T) {
	cases := []struct {
		s     string
		split int
	}{
		{"Forêt", 4},
		{"Ah ! Ça ira, ça ira !", 7},
		{"🇫🇷 🇺🇾 🇦🇷 🇺🇸 🇵🇹 🇮🇳 🇨🇦 🇬🇧", 2},
	}

	w := NewUTF8DanglingWriter(&bytes.Buffer{})

	for _, c := range cases {
		prev := 0
		next := c.split
		for {
			if next > len(c.s) {
				next = len(c.s)
			}

			s := c.s[prev:next]

			n, err := w.Write([]byte(s))
			require.NoError(t, err)
			require.Equal(t, len(s), n)

			if next == len(c.s) {
				break
			}

			prev = next
			next += c.split
		}
		require.Equal(t, c.s, w.w.(*bytes.Buffer).String())
		w.w.(*bytes.Buffer).Reset()
	}
}

func TestLineBreakWriter(t *testing.T) {
	cases := []struct {
		s     string
		split int
	}{
		{"Forêt\nArbre\nLol\n", 4},
		{"Ah !\n Ça ira,\n ça ira !\n", 7},
		{"🇫🇷\n🇺🇾\n🇦🇷\n🇺🇸\n🇵🇹\n🇮🇳\n🇨🇦\n🇬🇧\n", 2},
	}
	buf := bytes.Buffer{}
	w := NewLineBreakWriter(&buf)

	for _, c := range cases {
		prev := 0
		next := c.split
		for {
			if next > len(c.s) {
				next = len(c.s)
			}

			s := c.s[prev:next]

			n, err := w.Write([]byte(s))
			require.NoError(t, err)
			require.Equal(t, len(s), n)

			if next == len(c.s) {
				break
			}

			prev = next
			next += c.split
		}
		require.Equal(t, c.s, buf.String())
		buf.Reset()
	}
}

func TestContainsPartialSecret(t *testing.T) {
	t.Run("contains a partial of a secret", func(t *testing.T) {
		s := "mochi mochi, yu"
		secrets := []string{
			"tasty",
			"yummy", // yu could be the start of this secret
		}

		ok, idx := containsPartialSecret([]byte(s), secrets)
		require.True(t, ok)
		require.Equal(t, 13, idx)
	})
	t.Run("contains a full secret", func(t *testing.T) {
		s := "mochi mochi, yummy"
		secrets := []string{
			"tasty",
			"yummy",
		}

		ok, idx := containsPartialSecret([]byte(s), secrets)
		require.False(t, ok)
		require.Equal(t, -1, idx)
	})
	t.Run("contains no secret", func(t *testing.T) {
		s := "mochi mochi, yummy"
		secrets := []string{
			"tasty",
			"nothing",
		}

		ok, idx := containsPartialSecret([]byte(s), secrets)
		require.False(t, ok)
		require.Equal(t, -1, idx)
	})
}

func TestLineBreakWriter_writeDangling(t *testing.T) {
	var b bytes.Buffer
	lb := LineBreakWriter{
		w: &b,
		secretLines: []string{
			"tasty",
			"yummy",
		},
	}

	got := lb.writeDangling([]byte("sorry, it was yum"))
	require.Equal(t, "sorry, it was ", string(got))
	require.Equal(t, "yum", string(lb.buffer))
}
