package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"golang.org/x/sync/errgroup"
	"io"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
)

var (
	//nolint:typecheck
	//go:embed testdata/id_ed25519
	sshSecretKey string

	//nolint:typecheck
	//go:embed testdata/id_ed25519.pub
	sshPublicKey string
)

func TestSecretScrubWriterWrite(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"mysecret": &fstest.MapFile{
			Data: []byte("my secret file"),
		},
		"subdir/alsosecret": &fstest.MapFile{
			Data: []byte("a subdir secret file \nwith line feed"),
		},
	}
	env := []string{
		"MY_SECRET_ID=my secret value",
	}

	t.Run("scrub files and env", func(t *testing.T) {
		var buf bytes.Buffer
		buf.WriteString("I love to share my secret value to my close ones. But I keep my secret file to myself. As well as a subdir secret file \nwith line feed.")
		currentDirPath := "/"
		r, err := NewSecretScrubReader(&buf, currentDirPath, fsys, env, core.SecretToScrubInfo{
			Envs:  []string{"MY_SECRET_ID"},
			Files: []string{"/mysecret", "/subdir/alsosecret"},
		})
		require.NoError(t, err)
		out, err := io.ReadAll(r)
		require.NoError(t, err)
		want := "I love to share *** to my close ones. But I keep *** to myself. As well as ***."
		require.Equal(t, want, string(out))
	})

	t.Run("do not scrub empty env", func(t *testing.T) {
		env := append(env, "EMPTY_SECRET_ID=")
		currentDirPath := "/"
		fsys := fstest.MapFS{
			"emptysecret": &fstest.MapFile{
				Data: []byte(""),
			},
		}

		var buf bytes.Buffer
		buf.WriteString("I love to share my secret value to my close ones. But I keep my secret file to myself.")

		r, err := NewSecretScrubReader(&buf, currentDirPath, fsys, env, core.SecretToScrubInfo{
			Envs:  []string{"EMPTY_SECRET_ID"},
			Files: []string{"/emptysecret"},
		})
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

	secretToScrub := core.SecretToScrubInfo{
		Envs: []string{
			"MY_SECRET_ID",
		},
	}

	secrets := loadSecretsToScrubFromEnv(env, secretToScrub.Envs)
	require.NotContains(t, secrets, "PUBLIC_STUFF")
	require.Contains(t, secrets, secretValue)
}

func TestLoadSecretsToScrubFromFiles(t *testing.T) {
	t.Parallel()
	const currentDirPath = "/mnt"
	t.Run("/mnt, fs relative, secret absolute", func(t *testing.T) {
		fsys := fstest.MapFS{
			"mysecret": &fstest.MapFile{
				Data: []byte("my secret file"),
			},
			"subdir/alsosecret": &fstest.MapFile{
				Data: []byte("a subdir secret file"),
			},
		}
		secretFilePathsToScrub := []string{"/mnt/mysecret", "/mnt/subdir/alsosecret"}

		secrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretFilePathsToScrub)
		require.NoError(t, err)
		require.Contains(t, secrets, "my secret file")
		require.Contains(t, secrets, "a subdir secret file")
	})

	t.Run("/mnt, fs relative, secret relative", func(t *testing.T) {
		fsys := fstest.MapFS{
			"mysecret": &fstest.MapFile{
				Data: []byte("my secret file"),
			},
			"subdir/alsosecret": &fstest.MapFile{
				Data: []byte("a subdir secret file"),
			},
		}
		secretFilePathsToScrub := []string{"mysecret", "subdir/alsosecret"}

		secrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretFilePathsToScrub)
		require.NoError(t, err)
		require.Contains(t, secrets, "my secret file")
		require.Contains(t, secrets, "a subdir secret file")
	})

	t.Run("/mnt, fs absolute, secret relative", func(t *testing.T) {
		fsys := fstest.MapFS{
			"mnt/mysecret": &fstest.MapFile{
				Data: []byte("my secret file"),
			},
			"mnt/subdir/alsosecret": &fstest.MapFile{
				Data: []byte("a subdir secret file"),
			},
		}
		secretFilePathsToScrub := []string{"mnt/mysecret", "mnt/subdir/alsosecret"}

		secrets, err := loadSecretsToScrubFromFiles(currentDirPath, fsys, secretFilePathsToScrub)
		require.NoError(t, err)
		require.Contains(t, secrets, "my secret file")
		require.Contains(t, secrets, "a subdir secret file")
	})
}

func TestScrubSecretLogLatency(t *testing.T) {
	t.Parallel()

	for _, n := range []int{1, 10, 100, 500, 1500} {
		t.Run(fmt.Sprintf("Test log latency with %d secrets", n), func(t *testing.T) {
			envMap := map[string]string{}

			for line := 0; line < n; line++ {
				envMap[fmt.Sprintf("secret%d", line)] = fmt.Sprintf("secret%d value", line)
			}

			var (
				env, envNames,
				inputs, outputs []string
			)

			// Initializes envMap with parameters
			for k, v := range envMap {
				env = append(env, fmt.Sprintf("%s=%s", k, v))
				envNames = append(envNames, k)
				inputs = append(inputs, fmt.Sprintf("The %s is %s", k, v))
				outputs = append(outputs, fmt.Sprintf("The %s is ***", k))
			}

			secretToScrubInfo := core.SecretToScrubInfo{
				Envs:  envNames,
				Files: []string{},
			}

			// Create the buffer to read/write in
			var buf bytes.Buffer

			// Init channel to synchronize read/write go routine
			// The process will run in concurrency and block the read when a write happens
			// and vice-versa.
			read := make(chan bool)
			write := make(chan bool)

			// Close channels at the end.
			defer func() {
				close(read)
				close(write)
			}()

			output := strings.Join(outputs, "\n")

			// Create error group to synchronize go routine
			eg, _ := errgroup.WithContext(context.Background())

			// kick off a goroutine with an io.Reader which mocks a with_exec'ed process' stdout;
			// We will write each line of secret, one by one.
			eg.Go(func() error {
				// Create an incremental index to send lines one by one.
				idx := 0

				for range write {
					// Write the secret into the buffer.
					_, err := buf.WriteString(inputs[idx])
					require.NoError(t, err)

					idx += 1

					// Stop the go routine when we wrote all lines
					// We send a one last read request to read the last line.
					if idx == len(inputs) {
						read <- true
						return nil
					}

					// Ask to read the secret.
					read <- true
				}

				return nil
			})

			// Store result for final check.
			var result []string

			// concurrently, expect the io.Reader returned by NewSecretScrubReader
			// to read an expected subset of those bytes, then unblock the channel,
			// then read another expected subset of bytes, then unblock again.
			eg.Go(func() error {
				idx := 0

				for range read {
					// Read the buffer after write.
					r, err := NewSecretScrubReader(&buf, "/", fstest.MapFS{}, env, secretToScrubInfo)
					require.NoError(t, err)

					// Read out, we ignore error if we hit EOF.
					out, err := io.ReadAll(r)
					if err != nil {
						if !errors.Is(err, io.EOF) {
							require.NoError(t, err)
						}
					}

					// Make sure the output are matching as expected.
					require.Equal(t, outputs[idx], string(out))

					// Append the output to the final result for a global check.
					result = append(result, string(out))

					// We stop the go routine when we complete the outputs array.
					idx += 1
					if idx == len(outputs) {
						return nil
					}

					// Unblock write channel by sending a request.
					write <- true
				}

				return nil
			})

			// Kick off the process with a write request.
			write <- true

			err := eg.Wait()
			require.NoError(t, err)
			require.Equal(t, output, strings.Join(result, "\n"))
		})
	}
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

	envNames := []string{
		"secret1",
		"secret2",
		"sshSecretKey",
		"sshPublicKey",
	}

	secretToScrubInfo := core.SecretToScrubInfo{
		Envs:  envNames,
		Files: []string{},
	}

	t.Run("multiline secret", func(t *testing.T) {
		for input, expectedOutput := range map[string]string{
			"aaa\n" + sshSecretKey + "\nbbb\nccc": "aaa\n***\nbbb\nccc",
			"aaa" + sshSecretKey + "bbb\nccc":     "aaa***bbb\nccc",
			sshSecretKey:                          "***",
		} {
			var buf bytes.Buffer
			r, err := NewSecretScrubReader(&buf, "/", fstest.MapFS{}, env, secretToScrubInfo)
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
		r, err := NewSecretScrubReader(&buf, "/", fstest.MapFS{}, env, secretToScrubInfo)
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
		r, err := NewSecretScrubReader(&buf, "/", fstest.MapFS{}, env, secretToScrubInfo)
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
