package buildkit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/transform"
)

var (
	// scrubString will be used as replacement for found secrets:
	scrubString = []byte("***")
)

func NewSecretScrubReader(
	r io.Reader,
	env []string,
	secretEnvs []string,
	secretFiles []string,
) (io.Reader, error) {
	secrets := loadSecretsToScrubFromEnv(env, secretEnvs)

	fileSecrets, err := loadSecretsToScrubFromFiles(secretFiles)
	if err != nil {
		return nil, fmt.Errorf("could not load secrets from file: %w", err)
	}
	secrets = append(secrets, fileSecrets...)

	secretAsBytes := make([][]byte, 0)
	for _, v := range secrets {
		// Skip empty env:
		if len(v) == 0 {
			continue
		}
		secretAsBytes = append(secretAsBytes, []byte(v))
	}

	trie := &Trie{}
	for _, s := range secretAsBytes {
		trie.Insert(s, scrubString)
		if strimmed := bytes.TrimSpace(s); len(strimmed) != len(s) {
			trie.Insert(strimmed, scrubString)
		}
	}
	transformer := &censor{
		trieRoot: trie,
		trie:     trie.Iter(),
		// NOTE: keep these sizes the same as the default transform sizes
		srcBuf: make([]byte, 0, 4096),
		dstBuf: make([]byte, 0, 4096),
	}

	return transform.NewReader(r, transformer), nil
}

// loadSecretsToScrubFromEnv loads secrets value from env if they are in secretsToScrub.
func loadSecretsToScrubFromEnv(env []string, secretsToScrub []string) []string {
	secrets := []string{}

	for _, envKV := range env {
		envName, envValue, ok := strings.Cut(envKV, "=")
		// no env value for this secret
		if !ok {
			continue
		}

		for _, envToScrub := range secretsToScrub {
			if envName == envToScrub {
				secrets = append(secrets, envValue)
			}
		}
	}

	return secrets
}

// loadSecretsToScrubFromFiles loads secrets from file path in secretFilePathsToScrub, which must be absolute
func loadSecretsToScrubFromFiles(secretFilePathsToScrub []string) ([]string, error) {
	secrets := make([]string, 0, len(secretFilePathsToScrub))

	for _, fileToScrub := range secretFilePathsToScrub {
		if !filepath.IsAbs(fileToScrub) {
			return nil, fmt.Errorf("file path must be absolute: %s", fileToScrub)
		}

		secret, err := os.ReadFile(fileToScrub)
		if err != nil {
			return nil, fmt.Errorf("secret value not available for: %w", err)
		}
		secrets = append(secrets, string(secret))
	}

	return secrets, nil
}

// censor is a custom Transformer for replacing all keys in a target trie with
// their values.
type censor struct {
	// trieRoot is the root of the trie
	trieRoot *Trie
	// trie is the current node we are at in the trie
	trie *TrieIter
	// match is the last trie node that we found a match from
	match    *TrieIter
	matchLen int

	// srcBuf is the source buffer, which contains bytes read from the src that
	// are partial matches against the trie
	srcBuf []byte
	// destBuf is the destination buffer, which contains bytes that have been
	// sanitized by the censor and are ready to be copied out
	dstBuf []byte
}

// Transform ingests src bytes, and outputs sanitized bytes to dst.
//
// Unlike some other secret scrubbing implementations, this aims to sanitize
// bytes *as soon as possible*. The moment that we know a byte is not part of a
// secret, we should output it into dst - even if this would break up a provided
// src into multiple dsts over multiple calls to Transform.
func (c *censor) Transform(dst, src []byte, atEOF bool) (nDst, nSrc int, err error) {
	for {
		// flush the destination buffer
		k := copy(dst[nDst:], c.dstBuf)
		nDst += k
		if nDst == len(dst) {
			c.dstBuf = c.dstBuf[k:]
			return nDst, nSrc, transform.ErrShortDst
		}
		c.dstBuf = c.dstBuf[:0]

		if !atEOF && nSrc == len(src) {
			// no more source bytes, we're done!
			return nDst, nSrc, nil
		}
		if atEOF && nSrc == len(src) && len(c.srcBuf) == 0 {
			// no more source bytes, or buffered source bytes, we're done!
			// (when atEOF, we won't get called again, so we need to make sure
			// to flush everything)
			return nDst, nSrc, nil
		}

		// read more source bytes, until either we've read all the source
		// bytes, or we've filled the destination buffer
		for ; nSrc < len(src) && nDst+len(c.dstBuf) < len(dst); nSrc++ {
			ch := src[nSrc]
			c.trie = c.trie.Step(ch)

			if c.trie == nil {
				// we had found a match somewhere in this string previously, so
				// flush the secret replacement and the rest of the source
				// buffer
				if c.match != nil {
					c.trie = c.trieRoot.Iter()
					c.dstBuf = append(c.dstBuf, c.match.Value()...)
					c.dstBuf = append(c.dstBuf, c.srcBuf[c.matchLen:]...)
					c.srcBuf = c.srcBuf[:0]
					c.match = nil
					c.matchLen = 0

					// process the current byte again. we do this because this
					// *might* cause us to try to flush more than len(dst) - nDst
					// bytes into the destination buffer, so we should avoid
					// consuming the next byte in this case.
					nSrc--
					continue
				}

				// no match possible, so flush the source buffer into the
				// destination buffer
				if len(c.srcBuf) != 0 {
					c.trie = c.trieRoot.Iter()
					c.dstBuf = append(c.dstBuf, c.srcBuf...)
					c.srcBuf = c.srcBuf[:0]

					// process the current byte again - same reason as above
					nSrc--
					continue
				}

				// put the current byte either into the destination buffer, or
				// the source buffer, depending on whether it's a partial match
				c.trie = c.trieRoot.Step(ch)
				if c.trie == nil {
					c.trie = c.trieRoot.Iter()
					c.dstBuf = append(c.dstBuf, ch)
				} else if replace := c.trie.Value(); replace != nil {
					c.trie = c.trieRoot.Iter()
					c.dstBuf = append(c.dstBuf, replace...)
				} else {
					c.srcBuf = append(c.srcBuf, ch)
				}
			} else if replace := c.trie.Value(); replace != nil {
				// aha, we made a match, mark it, and we'll come back and flush
				// the censored string later
				c.srcBuf = append(c.srcBuf, ch)
				c.match = c.trie
				c.matchLen = len(c.srcBuf)
			} else {
				// we're in the middle of a match
				c.srcBuf = append(c.srcBuf, ch)
			}
		}

		// at this point, no more matches are possible, so flush
		if atEOF {
			if c.match != nil {
				c.dstBuf = append(c.dstBuf, c.match.Value()...)
				c.dstBuf = append(c.dstBuf, c.srcBuf[c.matchLen:]...)
				c.match = nil
				c.matchLen = 0
			} else {
				c.dstBuf = append(c.dstBuf, c.srcBuf...)
			}
			c.srcBuf = c.srcBuf[:0]
		}
	}
}

func (c *censor) Reset() {
	c.trie = c.trieRoot.Iter()
	c.srcBuf = c.srcBuf[:0]
	c.dstBuf = c.dstBuf[:0]
}

// Trie is a simple implementation of a compressed trie (or radix tree). In
// essence, it's a key-value store that allows easily selecting all entries
// that have a given prefix.
//
// Why not an off-the-shelf implementation? Well, most of those don't allow
// navigating character-by-character through the tree, like we do with Step.
type Trie struct {
	// value is the value stored in this trie node
	value []byte

	// children is a byte-indexed slice of child nodes
	children []*Trie
	// direct is a prefix that every child in this node has - this is the
	// compressed part of the compressed trie, and it saves us a huge amount of
	// memory and performance
	direct []byte
}

func (t *Trie) Iter() *TrieIter {
	return &TrieIter{Trie: t}
}

func (t *Trie) Insert(key []byte, value []byte) {
	t.Iter().insert(key, value)
}

func (t *Trie) Step(ch byte) *TrieIter {
	return t.Iter().Step(ch)
}

// String prints a debuggable representation of the trie.
func (t Trie) String() string {
	lines := ""
	lines += fmt.Sprintf("%s (%s)\n", t.direct, t.value)

	for ch, child := range t.children {
		if child != nil {
			lines += fmt.Sprintf("- %c ->\n", ch)
			for _, line := range strings.Split(child.String(), "\n") {
				lines += "  " + line + "\n"
			}
		}
	}
	return strings.TrimSpace(lines)
}

// TrieIter is an iterator that allows navigating through a Trie.
//
// This is used so that we can navigate through the compressed Trie structure
// easily - not every node "exists", but the TrieIter handles this case. For
// example, a node might have a direct of `foo`, so the node `fo` is virtual.
type TrieIter struct {
	*Trie

	// idx is the current index of this node into direct
	idx int
}

func (t *TrieIter) insert(key []byte, value []byte) {
	if t == nil {
		panic("cannot insert into nil tree")
	}

	if len(key) == 0 || t.direct == nil {
		// we're done, this is where we shall store the data!
		t = t.materialize().Iter()
		if t.direct == nil {
			t.direct = key
		}
		t.value = value
		return
	}

	next := t.Step(key[0])
	if next == nil {
		t = t.materialize().Iter()
		t.branch()
		child := t.children[key[0]]
		if child == nil {
			child = &Trie{}
			t.children[key[0]] = child
		}
		next = child.Iter()
	}

	next.insert(key[1:], value)
}

// materialize is the main magic of how insertion works.
//
// This function can take any iterable part of the trie, and if the node is
// virtual, then it will modify the trie to make it "real". This means that
// this node can then store data, or can be given it's own children.
func (t *TrieIter) materialize() *Trie {
	if t.idx == len(t.direct) {
		// already materialized
		return t.Trie
	}

	direct := t.direct
	child := &Trie{
		direct:   direct[t.idx+1:],
		children: t.children,
		value:    t.value,
	}
	t.direct = direct[:t.idx]
	t.children = nil
	t.value = nil

	t.branch()
	t.children[direct[t.idx]] = child

	return t.Trie
}

// branch takes a node in the trie and converts it from a leaf node into a
// branch node (if it wasn't already)
func (t *Trie) branch() {
	// why a slice instead of a map? surely it uses more space?
	// well, doing a lookup on a slice like this is *super* quick, but
	// doing so on a map is *much* slower - since this is in the
	// hotpath, it makes sense to waste the memory here (and since the
	// trie is compressed, it doesn't seem to be that much in practice)
	if t.children != nil {
		return
	}
	t.children = make([]*Trie, 256)
}

// Step selects a node that was previously inserted.
func (t *TrieIter) Step(ch byte) *TrieIter {
	if t == nil {
		return nil
	}
	if t.idx < len(t.direct) {
		if t.direct[t.idx] == ch {
			return &TrieIter{
				Trie: t.Trie,
				idx:  t.idx + 1,
			}
		}
		return nil
	}
	if t.children != nil {
		child := t.children[ch]
		if child != nil {
			return &TrieIter{Trie: child}
		}
	}
	return nil
}

// Value gets the value previously inserted at this node.
func (t *TrieIter) Value() []byte {
	if t == nil {
		return nil
	}
	if t.idx == len(t.direct) {
		return t.value
	}
	return nil
}
