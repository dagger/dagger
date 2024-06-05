package buildkit

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	//go:embed testdata/id_ed25519
	sshSecretKey string

	//go:embed testdata/id_ed25519.pub
	sshPublicKey string
)

func TestSecretScrubWriterWrite(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	mySecretPath := filepath.Join(tempDir, "mysecret")
	require.NoError(t, os.WriteFile(mySecretPath, []byte("my secret file"), 0600))
	subdirPath := filepath.Join(tempDir, "subdir")
	require.NoError(t, os.Mkdir(subdirPath, 0700))
	alsoSecretPath := filepath.Join(subdirPath, "alsosecret")
	require.NoError(t, os.WriteFile(alsoSecretPath, []byte("a subdir secret file \nwith line feed"), 0600))
	env := []string{
		"MY_SECRET_ID=my secret value",
	}

	t.Run("scrub files and env", func(t *testing.T) {
		var buf bytes.Buffer
		buf.WriteString("I love to share my secret value to my close ones. But I keep my secret file to myself. As well as a subdir secret file \nwith line feed.")
		r, err := NewSecretScrubReader(&buf, env,
			[]string{"MY_SECRET_ID"},
			[]string{mySecretPath, alsoSecretPath},
		)
		require.NoError(t, err)
		out, err := io.ReadAll(r)
		require.NoError(t, err)
		want := "I love to share *** to my close ones. But I keep *** to myself. As well as ***."
		require.Equal(t, want, string(out))
	})

	t.Run("do not scrub empty env", func(t *testing.T) {
		env := append(env, "EMPTY_SECRET_ID=")
		tempDir := t.TempDir()
		emptySecretPath := filepath.Join(tempDir, "emptysecret")
		require.NoError(t, os.WriteFile(emptySecretPath, []byte(""), 0600))

		var buf bytes.Buffer
		buf.WriteString("I love to share my secret value to my close ones. But I keep my secret file to myself.")

		r, err := NewSecretScrubReader(&buf, env,
			[]string{"EMPTY_SECRET_ID"},
			[]string{emptySecretPath},
		)
		require.NoError(t, err)
		out, err := io.ReadAll(r)
		require.NoError(t, err)
		want := "I love to share my secret value to my close ones. But I keep my secret file to myself."
		require.Equal(t, want, string(out))
	})
}

func TestLoadSecretsToScrubFromEnv(t *testing.T) {
	t.Parallel()
	secretValue := "my secret value"
	env := []string{
		fmt.Sprintf("MY_SECRET_ID=%s", secretValue),
		"PUBLIC_STUFF=so public",
	}

	secrets := loadSecretsToScrubFromEnv(env, []string{"MY_SECRET_ID"})
	require.NotContains(t, secrets, "PUBLIC_STUFF")
	require.Contains(t, secrets, secretValue)
}

func TestLoadSecretsToScrubFromFiles(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	mySecretPath := filepath.Join(tempDir, "mysecret")
	require.NoError(t, os.WriteFile(mySecretPath, []byte("my secret file"), 0600))
	subdirPath := filepath.Join(tempDir, "subdir")
	require.NoError(t, os.Mkdir(subdirPath, 0700))
	alsoSecretPath := filepath.Join(subdirPath, "alsosecret")
	require.NoError(t, os.WriteFile(alsoSecretPath, []byte("a subdir secret file"), 0600))

	secrets, err := loadSecretsToScrubFromFiles([]string{mySecretPath, alsoSecretPath})
	require.NoError(t, err)
	require.Contains(t, secrets, "my secret file")
	require.Contains(t, secrets, "a subdir secret file")
}

func TestScrubSecretWrite(t *testing.T) {
	t.Parallel()
	envMap := map[string]string{
		"secret1":      "secret1 value",
		"secret2":      "secret2",
		"sshSecretKey": sshSecretKey,
		"sshPublicKey": sshPublicKey,
	}

	env := []string{}
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	secretEnvs := []string{
		"secret1",
		"secret2",
		"sshSecretKey",
		"sshPublicKey",
	}

	t.Run("multiline secret", func(t *testing.T) {
		for input, expectedOutput := range map[string]string{
			"aaa\n" + sshSecretKey + "\nbbb\nccc": "aaa\n***\nbbb\nccc",
			"aaa" + sshSecretKey + "bbb\nccc":     "aaa***bbb\nccc",
			sshSecretKey:                          "***",
		} {
			var buf bytes.Buffer
			r, err := NewSecretScrubReader(&buf, env, secretEnvs, []string{})
			require.NoError(t, err)
			_, err = buf.WriteString(input)
			require.NoError(t, err)
			out, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Equal(t, expectedOutput, string(out))
		}
	})
	t.Run("single line secret", func(t *testing.T) {
		var buf bytes.Buffer
		r, err := NewSecretScrubReader(&buf, env, secretEnvs, []string{})
		require.NoError(t, err)

		input := "aaa\nsecret1 value\nno secret\n"
		_, err = buf.WriteString(input)
		require.NoError(t, err)
		out, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, "aaa\n***\nno secret\n", string(out))
	})

	t.Run("multi write", func(t *testing.T) {
		var buf bytes.Buffer
		r, err := NewSecretScrubReader(&buf, env, secretEnvs, []string{})
		require.NoError(t, err)

		inputLines := []string{
			"secret1 value",
			"secret2",
			"nonsecret",
		}
		outputLines := []string{
			"***",
			"***",
			"nonsecret",
		}

		// Do multi write:
		for _, s := range inputLines {
			buf.WriteString(s)
			buf.WriteRune('\n')
		}

		// Scan through SecretScrubReader and validate output:
		scanner := bufio.NewScanner(r)
		var i int
		for scanner.Scan() {
			out := scanner.Text()
			expected := outputLines[i]
			require.Equal(t, expected, out)
			i++
		}
		require.Equal(t, len(outputLines), i)
	})
}

func TestScrubSecretLogLatency(t *testing.T) {
	t.Parallel()
	envMap := map[string]string{
		"foo": "TOP_SECRET",
		"bar": strings.Repeat("a", 10_000),
		"baz": "x",
		"qux": "yyy",
	}
	env := []string{}
	secretEnvNames := []string{}
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
		secretEnvNames = append(secretEnvNames, k)
	}

	t.Run("plain", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte("hello world\n"))
			require.NoError(t, err)
			wg.Done()
		}()
		go func() {
			buf := make([]byte, 1024)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "hello world\n", string(buf[:n]))
			wg.Done()
		}()
		wg.Wait()
	})

	t.Run("secret", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte("hello TOP_SECRET\n"))
			require.NoError(t, err)
			wg.Done()
		}()
		go func() {
			buf := make([]byte, 1024)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "hello ***\n", string(buf[:n]))
			wg.Done()
		}()
		wg.Wait()
	})

	t.Run("secret double", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte("hello TOP_TOP_SECRETTOP_SECRETTOP_\n"))
			require.NoError(t, err)
			wg.Done()
		}()
		go func() {
			buf := make([]byte, 1024)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "hello TOP_******TOP_\n", string(buf[:n]))
			wg.Done()
		}()
		wg.Wait()
	})

	t.Run("secret one byte", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte("yyx\n"))
			require.NoError(t, err)
			wg.Done()
		}()
		go func() {
			buf := make([]byte, 1024)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "yy***\n", string(buf[:n]))
			wg.Done()
		}()
		wg.Wait()
	})

	t.Run("secret half", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte("hello TOP_"))
			require.NoError(t, err)
			_, err = out.Write([]byte("SECRET!\n"))
			require.NoError(t, err)
			wg.Done()
		}()
		go func() {
			buf := make([]byte, 1024)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "hello ", string(buf[:n]))
			n, err = r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "***!\n", string(buf[:n]))
			wg.Done()
		}()
		wg.Wait()
	})

	t.Run("eof", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte("hello TOP_"))
			require.NoError(t, err)
			err = out.Close()
			require.NoError(t, err)
			wg.Done()
		}()
		go func() {
			buf := make([]byte, 1024)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "hello ", string(buf[:n]))
			n, err = r.Read(buf)
			require.ErrorIs(t, err, io.EOF)
			require.Equal(t, "TOP_", string(buf[:n]))
			wg.Done()
		}()
		wg.Wait()
	})

	t.Run("massive", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte("hello\n" + strings.Repeat("a", 7000)))
			require.NoError(t, err)
			_, err = out.Write([]byte(strings.Repeat("a", 3001) + "\n"))
			require.NoError(t, err)

			_, err = out.Write([]byte("hello\n" + strings.Repeat("a", 7000)))
			require.NoError(t, err)
			_, err = out.Write([]byte(strings.Repeat("a", 2999) + "\n"))
			require.NoError(t, err)

			wg.Done()
		}()
		go func() {
			buf := make([]byte, 4096)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "hello\n", string(buf[:n]))
			n, err = r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "***a\n", string(buf[:n]))

			n, err = r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, "hello\n", string(buf[:n]))
			n, err = r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, strings.Repeat("a", 4096), string(buf[:n]))
			n, err = r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, strings.Repeat("a", 4096), string(buf[:n]))
			n, err = r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, strings.Repeat("a", 1807)+"\n", string(buf[:n]))

			wg.Done()
		}()
		wg.Wait()
	})

	t.Run("massive_eof", func(t *testing.T) {
		in, out := io.Pipe()
		r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
		require.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(2)
		go func() {
			_, err := out.Write([]byte(strings.Repeat("a", 9999)))
			require.NoError(t, err)
			err = out.Close()
			require.NoError(t, err)
			wg.Done()
		}()
		go func() {
			buf := make([]byte, 4096)
			n, err := r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, strings.Repeat("a", 4096), string(buf[:n]))
			n, err = r.Read(buf)
			require.NoError(t, err)
			require.Equal(t, strings.Repeat("a", 4096), string(buf[:n]))
			n, err = r.Read(buf)
			require.ErrorIs(t, err, io.EOF)
			require.Equal(t, strings.Repeat("a", 1807), string(buf[:n]))
			wg.Done()
		}()
		wg.Wait()
	})
}

func BenchmarkScrubSecret(b *testing.B) {
	envMap := map[string]string{
		"foo": strings.Repeat("a", 50),
		"bar": strings.Repeat("b", 50),
		"baz": strings.Repeat("c", 50),
		"qux": strings.Repeat("d", 50),
		"quu": strings.Repeat("e", 50),
		"quv": strings.Repeat("f", 50),
		"quw": strings.Repeat("g", 50),
		"quy": strings.Repeat("i", 50),
		"quz": strings.Repeat("j", 50),
	}
	env := []string{}
	secretEnvNames := []string{}
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
		secretEnvNames = append(secretEnvNames, k)
	}
	in, out := io.Pipe()
	r, err := NewSecretScrubReader(in, env, secretEnvNames, []string{})
	require.NoError(b, err)

	wg := sync.WaitGroup{}
	wg.Add(2)
	// randomly generate data to feed in
	go func() {
		for i := 0; i < b.N; i++ {
			// generate random data
			data := make([]byte, 4096)
			for i := range data {
				data[i] = byte(rand.Intn(256))
			}

			// insert some secret-like thing to the random data
			secret := []byte(strings.Repeat("a", rand.Intn(50)+25))
			idx := rand.Intn(len(data) - len(secret))
			copy(data[idx:], secret)

			// write to the pipe
			_, err := out.Write(data)
			require.NoError(b, err)
		}
		err := out.Close()
		require.NoError(b, err)

		wg.Done()
	}()
	go func() {
		data, err := io.ReadAll(r)
		require.NoError(b, err)
		require.NotContains(b, string(data), strings.Repeat("a", 50))

		wg.Done()
	}()

	wg.Wait()
}

func TestTrie(t *testing.T) {
	trie := Trie{}

	trie.Insert([]byte("foo"), []byte("bar"))
	fmt.Println(trie)
	require.Equal(t, []byte("bar"), trie.Step('f').Step('o').Step('o').Value())
	require.Nil(t, trie.Step('f').Step('o').Value())

	trie.Insert([]byte("fox"), []byte("bax"))
	fmt.Println(trie)
	require.Equal(t, []byte("bar"), trie.Step('f').Step('o').Step('o').Value())
	require.Equal(t, []byte("bax"), trie.Step('f').Step('o').Step('x').Value())

	trie.Insert([]byte("fax"), []byte("brx"))
	fmt.Println(trie)
	require.Equal(t, []byte("bar"), trie.Step('f').Step('o').Step('o').Value())
	require.Equal(t, []byte("bax"), trie.Step('f').Step('o').Step('x').Value())
	require.Equal(t, []byte("brx"), trie.Step('f').Step('a').Step('x').Value())
}

func TestTrieExtend(t *testing.T) {
	trie := Trie{}
	trie.Insert([]byte("foo"), []byte("bar"))
	trie.Insert([]byte("foob"), []byte("bax"))
	fmt.Println(trie)
	require.Equal(t, []byte("bar"), trie.Step('f').Step('o').Step('o').Value())
	require.Equal(t, []byte("bax"), trie.Step('f').Step('o').Step('o').Step('b').Value())

	trie = Trie{}
	trie.Insert([]byte("foob"), []byte("bax"))
	trie.Insert([]byte("foo"), []byte("bar"))
	fmt.Println(trie)
	require.Equal(t, []byte("bar"), trie.Step('f').Step('o').Step('o').Value())
	require.Equal(t, []byte("bax"), trie.Step('f').Step('o').Step('o').Step('b').Value())
}

func TestTrieReinsert(t *testing.T) {
	trie := Trie{}

	trie.Insert([]byte("foo"), []byte("bar"))
	require.Equal(t, []byte("bar"), trie.Step('f').Step('o').Step('o').Value())
	before := trie.String()

	trie.Insert([]byte("foo"), []byte("bar"))
	require.Equal(t, []byte("bar"), trie.Step('f').Step('o').Step('o').Value())
	after := trie.String()

	require.Equal(t, before, after)

	trie.Insert([]byte("foo"), []byte("baz"))
	require.Equal(t, []byte("baz"), trie.Step('f').Step('o').Step('o').Value())
}
