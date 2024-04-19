package system

import (
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestNormalizeWorkdir(t *testing.T) {
	testCases := []struct {
		name           string
		currentWorkdir string
		newWorkDir     string
		desiredResult  string
		err            string
	}{
		{
			name:           "no current wd with relative wd",
			currentWorkdir: "",
			newWorkDir:     "test",
			desiredResult:  `/test`,
			err:            "",
		},
		{
			name:           "no current wd with absolute wd",
			currentWorkdir: "",
			newWorkDir:     `/strippedWd`,
			desiredResult:  `/strippedWd`,
			err:            "",
		},
		{
			name:           "current wd is absolute, new wd is relative",
			currentWorkdir: "/test",
			newWorkDir:     `subdir`,
			desiredResult:  `/test/subdir`,
			err:            "",
		},
		{
			name:           "current wd is absolute, new wd is relative one folder up",
			currentWorkdir: "/test",
			newWorkDir:     `../subdir`,
			desiredResult:  `/subdir`,
			err:            "",
		},
		{
			name:           "current wd is absolute, new wd is absolute",
			currentWorkdir: "/test",
			newWorkDir:     `/current`,
			desiredResult:  `/current`,
			err:            "",
		},
		{
			name:           "current wd is relative, new wd is relative",
			currentWorkdir: "test",
			newWorkDir:     `current`,
			desiredResult:  `/test/current`,
			err:            "",
		},
		{
			name:           "current wd is relative, no new wd",
			currentWorkdir: "test",
			newWorkDir:     "",
			desiredResult:  `/test`,
			err:            "",
		},
		{
			name:           "current wd is absolute, no new wd",
			currentWorkdir: "/test",
			newWorkDir:     "",
			desiredResult:  `/test`,
			err:            "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := NormalizeWorkdir(tc.currentWorkdir, tc.newWorkDir, "linux")
			if tc.err != "" {
				require.EqualError(t, errors.Cause(err), tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.desiredResult, result)
		})
	}
}

// TestCheckSystemDriveAndRemoveDriveLetter tests CheckSystemDriveAndRemoveDriveLetter
func TestCheckSystemDriveAndRemoveDriveLetter(t *testing.T) {
	// Fails if not C drive.
	_, err := CheckSystemDriveAndRemoveDriveLetter(`d:\`, "windows")
	if err == nil || (err != nil && err.Error() != "The specified path is not on the system drive (C:)") {
		t.Fatalf("Expected error for d:")
	}

	var path string

	// Single character is unchanged
	if path, err = CheckSystemDriveAndRemoveDriveLetter("z", "windows"); err != nil {
		t.Fatalf("Single character should pass")
	}
	if path != "z" {
		t.Fatalf("Single character should be unchanged")
	}

	// Two characters without colon is unchanged
	if path, err = CheckSystemDriveAndRemoveDriveLetter("AB", "windows"); err != nil {
		t.Fatalf("2 characters without colon should pass")
	}
	if path != "AB" {
		t.Fatalf("2 characters without colon should be unchanged")
	}

	// Abs path without drive letter
	if path, err = CheckSystemDriveAndRemoveDriveLetter(`\l`, "windows"); err != nil {
		t.Fatalf("abs path no drive letter should pass")
	}
	if path != `/l` {
		t.Fatalf("abs path without drive letter should be unchanged")
	}

	// Abs path without drive letter, linux style
	if path, err = CheckSystemDriveAndRemoveDriveLetter(`/l`, "windows"); err != nil {
		t.Fatalf("abs path no drive letter linux style should pass")
	}
	if path != `/l` {
		t.Fatalf("abs path without drive letter linux failed %s", path)
	}

	// Drive-colon should be stripped
	if path, err = CheckSystemDriveAndRemoveDriveLetter(`c:\`, "windows"); err != nil {
		t.Fatalf("An absolute path should pass")
	}
	if path != `/` {
		t.Fatalf(`An absolute path should have been shortened to \ %s`, path)
	}

	// Verify with a linux-style path
	if path, err = CheckSystemDriveAndRemoveDriveLetter(`c:/`, "windows"); err != nil {
		t.Fatalf("An absolute path should pass")
	}
	if path != `/` {
		t.Fatalf(`A linux style absolute path should have been shortened to \ %s`, path)
	}

	// Failure on c:
	if path, err = CheckSystemDriveAndRemoveDriveLetter(`c:`, "windows"); err == nil {
		t.Fatalf("c: should fail")
	}
	if err.Error() != `No relative path specified in "c:"` {
		t.Fatalf(path, err)
	}

	// Failure on d:
	if path, err = CheckSystemDriveAndRemoveDriveLetter(`d:`, "windows"); err == nil {
		t.Fatalf("c: should fail")
	}
	if err.Error() != `No relative path specified in "d:"` {
		t.Fatalf(path, err)
	}

	// UNC path should fail.
	if _, err = CheckSystemDriveAndRemoveDriveLetter(`\\.\C$\test`, "windows"); err == nil {
		t.Fatalf("UNC path should fail")
	}
}

// TestNormalizeWorkdir tests NormalizeWorkdir
func TestNormalizeWorkdirWindows(t *testing.T) {
	testCases := []struct {
		name           string
		currentWorkdir string
		newWorkDir     string
		desiredResult  string
		err            string
	}{
		{
			name:           "no current wd with relative wd",
			currentWorkdir: "",
			newWorkDir:     "test",
			desiredResult:  `\test`,
			err:            "",
		},
		{
			name:           "no current wd with stripped absolute wd",
			currentWorkdir: "",
			newWorkDir:     `\strippedWd`,
			desiredResult:  `\strippedWd`,
			err:            "",
		},
		{
			name:           "no current wd with absolute wd",
			currentWorkdir: "",
			newWorkDir:     `C:\withDriveLetter`,
			desiredResult:  `\withDriveLetter`,
			err:            "",
		},
		{
			name:           "no current wd with absolute wd with forward slash",
			currentWorkdir: "",
			newWorkDir:     `C:/withDriveLetterAndForwardSlash`,
			desiredResult:  `\withDriveLetterAndForwardSlash`,
			err:            "",
		},
		{
			name:           "no current wd with absolute wd with mixed slashes",
			currentWorkdir: "",
			newWorkDir:     `C:/first\second/third`,
			desiredResult:  `\first\second\third`,
			err:            "",
		},
		{
			name:           "current wd is relative no wd",
			currentWorkdir: "testing",
			newWorkDir:     "",
			desiredResult:  `\testing`,
			err:            "",
		},
		{
			name:           "current wd is relative with relative wd",
			currentWorkdir: "testing",
			newWorkDir:     "newTesting",
			desiredResult:  `\testing\newTesting`,
			err:            "",
		},
		{
			name:           "current wd is relative withMixedSlashes and relative new wd",
			currentWorkdir: `testing/with\mixed/slashes`,
			newWorkDir:     "newTesting",
			desiredResult:  `\testing\with\mixed\slashes\newTesting`,
			err:            "",
		},
		{
			name:           "current wd is absolute withMixedSlashes and relative new wd",
			currentWorkdir: `C:\testing/with\mixed/slashes`,
			newWorkDir:     "newTesting",
			desiredResult:  `\testing\with\mixed\slashes\newTesting`,
			err:            "",
		},
		{
			name:           "current wd is absolute withMixedSlashes and no new wd",
			currentWorkdir: `C:\testing/with\mixed/slashes`,
			newWorkDir:     "",
			desiredResult:  `\testing\with\mixed\slashes`,
			err:            "",
		},
		{
			name:           "current wd is absolute path to non C drive",
			currentWorkdir: `D:\IWillErrorOut`,
			newWorkDir:     "doesNotMatter",
			desiredResult:  "",
			err:            "The specified path is not on the system drive (C:)",
		},
		{
			name:           "new WD is an absolute path to illegal drive",
			currentWorkdir: `C:\testing`,
			newWorkDir:     `D:\testing`,
			desiredResult:  "",
			err:            "The specified path is not on the system drive (C:)",
		},
		{
			name:           "current WD has no relative path to drive",
			currentWorkdir: `C:`,
			newWorkDir:     `testing`,
			desiredResult:  "",
			err:            `No relative path specified in "C:"`,
		},
		{
			name:           "new WD has no relative path to drive",
			currentWorkdir: `/test`,
			newWorkDir:     `C:`,
			desiredResult:  "",
			err:            `No relative path specified in "C:"`,
		},
		{
			name:           "new WD has no slash after drive letter",
			currentWorkdir: `/test`,
			newWorkDir:     `C:testing`,
			desiredResult:  `\test\testing`,
			err:            "",
		},
		{
			name:           "current WD is an unlikely absolute path",
			currentWorkdir: `C:\..\test\..\`,
			newWorkDir:     ``,
			desiredResult:  `\`,
			err:            "",
		},
		{
			name:           "linux style paths should work",
			currentWorkdir: "/test",
			newWorkDir:     "relative/path",
			desiredResult:  `\test\relative\path`,
			err:            "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := NormalizeWorkdir(tc.currentWorkdir, tc.newWorkDir, "windows")
			if tc.err != "" {
				require.EqualError(t, errors.Cause(err), tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.desiredResult, result)
		})
	}
}

func TestNormalizeWorkdirUnix(t *testing.T) {
	testCases := []struct {
		name           string
		currentWorkdir string
		newWorkDir     string
		desiredResult  string
		err            string
	}{
		{
			name:           "no current wd with relative wd",
			currentWorkdir: "",
			newWorkDir:     "test",
			desiredResult:  `/test`,
			err:            "",
		},
		{
			name:           "no current wd with absolute wd",
			currentWorkdir: "",
			newWorkDir:     `/strippedWd`,
			desiredResult:  `/strippedWd`,
			err:            "",
		},
		{
			name:           "current wd is relative no wd",
			currentWorkdir: "testing",
			newWorkDir:     "",
			desiredResult:  `/testing`,
			err:            "",
		},
		{
			name:           "current wd is relative with relative wd",
			currentWorkdir: "testing",
			newWorkDir:     "newTesting",
			desiredResult:  `/testing/newTesting`,
			err:            "",
		},
		{
			name:           "absolute current wd with relative new wd",
			currentWorkdir: "/test",
			newWorkDir:     "relative/path",
			desiredResult:  `/test/relative/path`,
			err:            "",
		},
		{
			name:           "absolute current wd with no new wd",
			currentWorkdir: "/test",
			newWorkDir:     "",
			desiredResult:  `/test`,
			err:            "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := NormalizeWorkdir(tc.currentWorkdir, tc.newWorkDir, "linux")
			if tc.err != "" {
				require.EqualError(t, errors.Cause(err), tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.desiredResult, result)
		})
	}
}

func TestIsAbs(t *testing.T) {
	testCases := []struct {
		name          string
		path          string
		desiredResult bool
	}{
		{
			name:          "path with drive letter is absolute",
			path:          `C:\test`,
			desiredResult: true,
		},
		{
			name:          "path with drive letter but no slash is relative",
			path:          `C:test`,
			desiredResult: false,
		},
		{
			name:          "path with drive letter and linux style slashes is absolute",
			path:          `C:/test`,
			desiredResult: true,
		},
		{
			name:          "path without drive letter but with leading slash is absolute",
			path:          `\test`,
			desiredResult: true,
		},
		{
			name:          "path without drive letter but with leading forward slash is absolute",
			path:          `/test`,
			desiredResult: true,
		},
		{
			name:          "simple relative path",
			path:          `test`,
			desiredResult: false,
		},
		{
			name:          "deeper relative path",
			path:          `test/nested`,
			desiredResult: false,
		},
		{
			name:          "one level up relative path",
			path:          `../test`,
			desiredResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsAbs(tc.path, "windows")
			require.Equal(t, tc.desiredResult, result)
		})
	}
}
