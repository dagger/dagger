package shell

import (
	"bufio"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConvertShellPatternToRegex(t *testing.T) {
	cases := map[string]string{
		"*":                       "^.*",
		"?":                       "^.",
		"\\*":                     "^\\*",
		"(()[]{\\}^$.\\*\\?|\\\\": "^\\(\\(\\)\\[\\]\\{\\}\\^\\$\\.\\*\\?\\|\\\\",
	}
	for pattern, expected := range cases {
		res, err := convertShellPatternToRegex(pattern, true, true)
		require.NoError(t, err)
		require.Equal(t, expected, res.String())
	}
	invalid := []string{
		"\\", "\\x", "\\\\\\",
	}
	for _, pattern := range invalid {
		_, err := convertShellPatternToRegex(pattern, true, true)
		require.Error(t, err)
	}
}

func TestReverseString(t *testing.T) {
	require.Equal(t, "12345", reverseString("54321"))
	require.Equal(t, "👽🚀🖖", reverseString("🖖🚀👽"))
}

func TestReversePattern(t *testing.T) {
	cases := map[string]string{
		"a\\*c":    "c\\*a",
		"\\\\\\ab": "b\\a\\\\",
		"ab\\":     "\\ba",
		"👽\\🚀🖖":    "🖖\\🚀👽",
		"\\\\b":    "b\\\\",
	}
	for pattern, expected := range cases {
		require.Equal(t, expected, reversePattern(pattern))
	}
}

func TestShellParserMandatoryEnvVars(t *testing.T) {
	var newWord string
	var err error
	shlex := NewLex('\\')
	setEnvs := []string{"VAR=plain", "ARG=x"}
	emptyEnvs := []string{"VAR=", "ARG=x"}
	unsetEnvs := []string{"ARG=x"}

	noEmpty := "${VAR:?message here$ARG}"
	noUnset := "${VAR?message here$ARG}"

	// disallow empty
	newWord, err = shlex.ProcessWord(noEmpty, setEnvs)
	require.NoError(t, err)
	require.Equal(t, "plain", newWord)

	_, err = shlex.ProcessWord(noEmpty, emptyEnvs)
	require.ErrorContains(t, err, "message herex")

	_, err = shlex.ProcessWord(noEmpty, unsetEnvs)
	require.ErrorContains(t, err, "message herex")

	// disallow unset
	newWord, err = shlex.ProcessWord(noUnset, setEnvs)
	require.NoError(t, err)
	require.Equal(t, "plain", newWord)

	newWord, err = shlex.ProcessWord(noUnset, emptyEnvs)
	require.NoError(t, err)
	require.Empty(t, newWord)

	_, err = shlex.ProcessWord(noUnset, unsetEnvs)
	require.ErrorContains(t, err, "message herex")
}

func TestShellParser4EnvVars(t *testing.T) {
	fn := "envVarTest"
	lineCount := 0

	file, err := os.Open(fn)
	require.NoError(t, err)
	defer file.Close()

	shlex := NewLex('\\')
	scanner := bufio.NewScanner(file)
	envs := []string{"PWD=/home", "SHELL=bash", "KOREAN=한국어", "NULL="}
	envsMap := BuildEnvs(envs)
	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		// Skip comments and blank lines
		if strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		words := strings.Split(line, "|")
		require.Equal(t, 3, len(words))

		platform := strings.TrimSpace(words[0])
		source := strings.TrimSpace(words[1])
		expected := strings.TrimSpace(words[2])

		// Key W=Windows; A=All; U=Unix
		if platform != "W" && platform != "A" && platform != "U" {
			t.Fatalf("Invalid tag %s at line %d of %s. Must be W, A or U", platform, lineCount, fn)
		}

		if ((platform == "W" || platform == "A") && runtime.GOOS == "windows") ||
			((platform == "U" || platform == "A") && runtime.GOOS != "windows") {
			newWord, err := shlex.ProcessWord(source, envs)
			if expected == "error" {
				require.Errorf(t, err, "input: %q, result: %q", source, newWord)
			} else {
				require.NoError(t, err, "at line %d of %s", lineCount, fn)
				require.Equal(t, expected, newWord, "at line %d of %s", lineCount, fn)
			}

			newWord, err = shlex.ProcessWordWithMap(source, envsMap)
			if expected == "error" {
				require.Errorf(t, err, "input: %q, result: %q", source, newWord)
			} else {
				require.NoError(t, err, "at line %d of %s", lineCount, fn)
				require.Equal(t, expected, newWord, "at line %d of %s", lineCount, fn)
			}
		}
	}
}

func TestShellParser4Words(t *testing.T) {
	fn := "wordsTest"

	file, err := os.Open(fn)
	if err != nil {
		t.Fatalf("Can't open '%s': %s", err, fn)
	}
	defer file.Close()

	const (
		modeNormal = iota
		modeOnlySetEnv
	)
	for _, mode := range []int{modeNormal, modeOnlySetEnv} {
		var envs []string
		shlex := NewLex('\\')
		if mode == modeOnlySetEnv {
			shlex.RawQuotes = true
			shlex.SkipUnsetEnv = true
		}
		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			line := scanner.Text()
			lineNum = lineNum + 1

			if strings.HasPrefix(line, "#") {
				continue
			}

			if strings.HasPrefix(line, "ENV ") {
				line = strings.TrimLeft(line[3:], " ")
				envs = append(envs, line)
				continue
			}

			words := strings.Split(line, "|")
			if len(words) != 2 {
				t.Fatalf("Error in '%s'(line %d) - should be exactly one | in: %q", fn, lineNum, line)
			}
			test := strings.TrimSpace(words[0])
			expected := strings.Split(strings.TrimLeft(words[1], " "), ",")

			// test for ProcessWords
			result, err := shlex.ProcessWords(test, envs)

			if err != nil {
				result = []string{"error"}
			}

			if len(result) != len(expected) {
				t.Fatalf("Error on line %d. %q was suppose to result in %q, but got %q instead", lineNum, test, expected, result)
			}
			for i, w := range expected {
				if w != result[i] {
					t.Fatalf("Error on line %d. %q was suppose to result in %q, but got %q instead", lineNum, test, expected, result)
				}
			}

			// test for ProcessWordsWithMap
			result, err = shlex.ProcessWordsWithMap(test, BuildEnvs(envs))

			if err != nil {
				result = []string{"error"}
			}

			if len(result) != len(expected) {
				t.Fatalf("Error on line %d. %q was suppose to result in %q, but got %q instead", lineNum, test, expected, result)
			}
			for i, w := range expected {
				if w != result[i] {
					t.Fatalf("Error on line %d. %q was suppose to result in %q, but got %q instead", lineNum, test, expected, result)
				}
			}
		}
	}
}

func TestGetEnv(t *testing.T) {
	sw := &shellWord{envs: nil, matches: make(map[string]struct{})}

	getEnv := func(name string) string {
		value, _ := sw.getEnv(name)
		return value
	}
	sw.envs = BuildEnvs([]string{})
	if getEnv("foo") != "" {
		t.Fatal("2 - 'foo' should map to ''")
	}

	sw.envs = BuildEnvs([]string{"foo"})
	if getEnv("foo") != "" {
		t.Fatal("3 - 'foo' should map to ''")
	}

	sw.envs = BuildEnvs([]string{"foo="})
	if getEnv("foo") != "" {
		t.Fatal("4 - 'foo' should map to ''")
	}

	sw.envs = BuildEnvs([]string{"foo=bar"})
	if getEnv("foo") != "bar" {
		t.Fatal("5 - 'foo' should map to 'bar'")
	}

	sw.envs = BuildEnvs([]string{"foo=bar", "car=hat"})
	if getEnv("foo") != "bar" {
		t.Fatal("6 - 'foo' should map to 'bar'")
	}
	if getEnv("car") != "hat" {
		t.Fatal("7 - 'car' should map to 'hat'")
	}

	// Make sure we grab the last 'car' in the list
	sw.envs = BuildEnvs([]string{"foo=bar", "car=hat", "car=bike"})
	if getEnv("car") != "bike" {
		t.Fatal("8 - 'car' should map to 'bike'")
	}
}

func TestProcessWithMatches(t *testing.T) {
	shlex := NewLex('\\')

	tc := []struct {
		input       string
		envs        map[string]string
		expected    string
		expectedErr bool
		matches     map[string]struct{}
	}{
		{
			input:    "x",
			envs:     map[string]string{"DUMMY": "dummy"},
			expected: "x",
			matches:  nil,
		},
		{
			input:    "x ${UNUSED}",
			envs:     map[string]string{"DUMMY": "dummy"},
			expected: "x ",
			matches:  nil,
		},
		{
			input:    "x ${FOO}",
			envs:     map[string]string{"FOO": "y"},
			expected: "x y",
			matches:  map[string]struct{}{"FOO": {}},
		},

		{
			input: "${FOO-aaa} ${BAR-bbb} ${BAZ-ccc}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expected: "xxx  ccc",
			matches:  map[string]struct{}{"FOO": {}, "BAR": {}},
		},
		{
			input: "${FOO:-aaa} ${BAR:-bbb} ${BAZ:-ccc}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expected: "xxx bbb ccc",
			matches:  map[string]struct{}{"FOO": {}, "BAR": {}},
		},

		{
			input: "${FOO+aaa} ${BAR+bbb} ${BAZ+ccc}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expected: "aaa bbb ",
			matches:  map[string]struct{}{"FOO": {}, "BAR": {}},
		},
		{
			input: "${FOO:+aaa} ${BAR:+bbb} ${BAZ:+ccc}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expected: "aaa  ",
			matches:  map[string]struct{}{"FOO": {}, "BAR": {}},
		},

		{
			input: "${FOO?aaa}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expected: "xxx",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input: "${BAR?bbb}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expected: "",
			matches:  map[string]struct{}{"BAR": {}},
		},
		{
			input: "${BAZ?ccc}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expectedErr: true,
		},
		{
			input: "${FOO:?aaa}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expected: "xxx",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input: "${BAR:?bbb}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expectedErr: true,
		},
		{
			input: "${BAZ:?ccc}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expectedErr: true,
		},

		{
			input: "${FOO=aaa}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expectedErr: true,
		},
		{
			input: "${FOO=:aaa}",
			envs: map[string]string{
				"FOO": "xxx",
				"BAR": "",
			},
			expectedErr: true,
		},
		{
			// special characters in regular expressions
			// } needs to be escaped so it doesn't match the
			// closing brace of ${}
			input:    "${FOO#()[]{\\}^$.\\*\\?|\\\\}",
			envs:     map[string]string{"FOO": "()[]{}^$.*?|\\x"},
			expected: "x",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input:    "${FOO%%\\**}",
			envs:     map[string]string{"FOO": "xx**"},
			expected: "xx",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input:    "${FOO#*x*y}",
			envs:     map[string]string{"FOO": "xxyy"},
			expected: "y",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input:    "${FOO##*x}",
			envs:     map[string]string{"FOO": "xxyy"},
			expected: "yy",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input:    "${FOO#?\\?}",
			envs:     map[string]string{"FOO": "???y"},
			expected: "?y",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input:    "${ABC:-.}${FOO%x}${ABC:-.}",
			envs:     map[string]string{"FOO": "xxyy"},
			expected: ".xxyy.",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			input:    "${FOO%%\\**\\*}",
			envs:     map[string]string{"FOO": "a***yy*"},
			expected: "a",
			matches:  map[string]struct{}{"FOO": {}},
		},
		{
			// test: wildcards
			input:    "${FOO/$NEEDLE/.} - ${FOO//$NEEDLE/.}",
			envs:     map[string]string{"FOO": "/foo*/*/*.txt", "NEEDLE": "\\*/"},
			expected: "/foo.*/*.txt - /foo..*.txt",
			matches:  map[string]struct{}{"FOO": {}, "NEEDLE": {}},
		},
		{
			// test: / in patterns
			input:    "${FOO/$NEEDLE/} - ${FOO//$NEEDLE/}",
			envs:     map[string]string{"FOO": "/tmp/tmp/bar.txt", "NEEDLE": "/tmp"},
			expected: "/tmp/bar.txt - /bar.txt",
			matches:  map[string]struct{}{"FOO": {}, "NEEDLE": {}},
		},
		{
			input:    "${FOO/$NEEDLE/$REPLACEMENT} - ${FOO//$NEEDLE/$REPLACEMENT}",
			envs:     map[string]string{"FOO": "/a/foo/b/c.txt", "NEEDLE": "/?/", "REPLACEMENT": "/"},
			expected: "/foo/b/c.txt - /foo/c.txt",
			matches:  map[string]struct{}{"FOO": {}, "NEEDLE": {}, "REPLACEMENT": {}},
		},
		{
			input:    "${FOO/$NEEDLE/$REPLACEMENT}",
			envs:     map[string]string{"FOO": "http://google.de", "NEEDLE": "http://", "REPLACEMENT": "https://"},
			expected: "https://google.de",
			matches:  map[string]struct{}{"FOO": {}, "NEEDLE": {}, "REPLACEMENT": {}},
		},
		{
			// test: substitute escaped separator characters
			input:    "${FOO//\\//\\/}",
			envs:     map[string]string{"FOO": "/tmp/foo.txt"},
			expected: "\\/tmp\\/foo.txt",
			matches:  map[string]struct{}{"FOO": {}},
		},
	}

	for _, c := range tc {
		c := c
		t.Run(c.input, func(t *testing.T) {
			w, matches, err := shlex.ProcessWordWithMatches(c.input, c.envs)
			if c.expectedErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.expected, w)

			require.Equal(t, len(c.matches), len(matches))
			for k := range c.matches {
				require.Contains(t, matches, k)
			}
		})
	}
}

func TestProcessWithMatchesPlatform(t *testing.T) {
	shlex := NewLex('\\')

	const (
		// corresponds to the filename convention used in https://github.com/moby/buildkit/releases
		release = "something-${VERSION}.${TARGETOS}-${TARGETARCH}${TARGETVARIANT:+-${TARGETVARIANT}}.tar.gz"
		version = "v1.2.3"
	)

	w, _, err := shlex.ProcessWordWithMatches(release, map[string]string{
		"VERSION":       version,
		"TARGETOS":      "linux",
		"TARGETARCH":    "arm",
		"TARGETVARIANT": "v7",
	})
	require.NoError(t, err)
	require.Equal(t, "something-v1.2.3.linux-arm-v7.tar.gz", w)

	w, _, err = shlex.ProcessWordWithMatches(release, map[string]string{
		"VERSION":       version,
		"TARGETOS":      "linux",
		"TARGETARCH":    "arm64",
		"TARGETVARIANT": "",
	})
	require.NoError(t, err)
	require.Equal(t, "something-v1.2.3.linux-arm64.tar.gz", w)

	w, _, err = shlex.ProcessWordWithMatches(release, map[string]string{
		"VERSION":    version,
		"TARGETOS":   "linux",
		"TARGETARCH": "arm64",
		// No "TARGETVARIANT": "",
	})
	require.NoError(t, err)
	require.Equal(t, "something-v1.2.3.linux-arm64.tar.gz", w)
}
