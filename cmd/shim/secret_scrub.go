package main

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"golang.org/x/text/transform"
)

var (
	// scrubString will be used as replacement for found secrets:
	scrubString = []byte("***")
)

func NewSecretScrubReader(r io.Reader, currentDirPath string, fsys fs.FS, env []string, secretsToScrub core.SecretToScrubInfo) (io.Reader, error) {
	secrets := loadSecretsToScrubFromEnv(env, secretsToScrub.Envs)

	fileSecrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretsToScrub.Files)
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
		trie.Insert([]byte(s), scrubString)
	}
	transformer := &censor{
		trie:     trie,
		trieRoot: trie,
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

// loadSecretsToScrubFromFiles loads secrets from file path in secretFilePathsToScrub from the fsys, accessed from the absolute currentDirPathAbs.
// It will attempt to make any file path as absolute file path by joining it with the currentDirPathAbs if need be.
func loadSecretsToScrubFromFiles(currentDirPathAbs string, fsys fs.FS, secretFilePathsToScrub []string) ([]string, error) {
	secrets := make([]string, 0, len(secretFilePathsToScrub))

	for _, fileToScrub := range secretFilePathsToScrub {
		absFileToScrub := fileToScrub
		if !filepath.IsAbs(fileToScrub) {
			absFileToScrub = filepath.Join("/", fileToScrub)
		}
		if strings.HasPrefix(fileToScrub, currentDirPathAbs) || strings.HasPrefix(fileToScrub, currentDirPathAbs[1:]) {
			absFileToScrub = strings.TrimPrefix(fileToScrub, currentDirPathAbs)
			absFileToScrub = filepath.Join("/", absFileToScrub)
		}

		// we remove the first `/` from the absolute path to  fileToScrub to work with fs.ReadFile
		secret, err := fs.ReadFile(fsys, absFileToScrub[1:])
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
	trie *Trie

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
// secret, we should ouput it into dst - even if this would break up a provided
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
				// no match possible, so flush the source buffer into the
				// destination buffer, and process the current byte again.
				//
				// we do this because this *might* cause us to try to flush
				// more than len(dst) - nDst bytes into the destination buffer,
				// so we should avoid consuming the next byte in this case.
				if len(c.srcBuf) != 0 {
					c.trie = c.trieRoot
					c.dstBuf = append(c.dstBuf, c.srcBuf...)
					c.srcBuf = c.srcBuf[:0]
					nSrc--
					continue
				}

				// put the current byte either into the destination buffer, or
				// the source buffer, depending on whether it's a partial match
				c.trie = c.trieRoot.Step(ch)
				if c.trie == nil {
					c.trie = c.trieRoot
					c.dstBuf = append(c.dstBuf, ch)
				} else if replace := c.trie.Value(); replace != nil {
					c.trie = c.trieRoot
					c.dstBuf = append(c.dstBuf, replace...)
				} else {
					c.srcBuf = append(c.srcBuf, ch)
				}
			} else if replace := c.trie.Value(); replace != nil {
				// aha, we made a match, so replace the source buffer with the
				// censored string, and flush into the destination buffer
				c.trie = c.trieRoot
				c.dstBuf = append(c.dstBuf, replace...)
				c.srcBuf = c.srcBuf[:0]
			} else {
				// we're in the middle of a match
				c.srcBuf = append(c.srcBuf, ch)
			}
		}

		// at this point, no more matches are possible, so flush
		if atEOF {
			c.dstBuf = append(c.dstBuf, c.srcBuf...)
			c.srcBuf = c.srcBuf[:0]
		}
	}
}

func (c *censor) Reset() {
	c.trie = c.trieRoot
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
	children []*Trie
	direct   []byte
	value    []byte
}

func (t *Trie) Insert(key []byte, value []byte) {
	node := t
	for i, ch := range key {
		if node.children == nil {
			if len(node.direct) == 0 {
				node.direct = key[i:]
				break
			}

			// why a slice instead of a map? surely it uses more space?
			// well, doing a lookup on a slice like this is *super* quick, but
			// doing so on a map is *much* slower - since this is in the
			// hotpath, it makes sense to waste the memory here (and since the
			// trie is compressed, it doesn't seem to be that much in practice)
			node.children = make([]*Trie, 256)
			node.children[node.direct[0]] = &Trie{
				direct: node.direct[1:],
				value:  node.value,
			}
			node.direct = nil
			node.value = nil
		}
		if node.children[ch] == nil {
			node.children[ch] = &Trie{}
		}
		node = node.children[ch]
	}
	node.value = value
}

func (t *Trie) Step(ch byte) *Trie {
	if t.children != nil {
		return t.children[ch]
	}
	if len(t.direct) > 0 && t.direct[0] == ch {
		return &Trie{
			direct: t.direct[1:],
			value:  t.value,
		}
	}
	return nil
}

func (t *Trie) Value() []byte {
	if len(t.direct) == 0 {
		return t.value
	}
	return nil
}
