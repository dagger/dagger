package pathutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLexicalRelativePath(t *testing.T) {
	tests := []struct {
		name     string
		cwdPath  string
		modPath  string
		expected string
		wantErr  bool
	}{
		{
			name:     "Simple relative path",
			cwdPath:  "/home/user",
			modPath:  "/home/user/project",
			expected: "project",
		},
		{
			name:     "Parent directory",
			cwdPath:  "/home/user/project",
			modPath:  "/home/user",
			expected: "..",
		},
		{
			name:     "Same directory",
			cwdPath:  "/home/user",
			modPath:  "/home/user",
			expected: ".",
		},
		{
			name:     "Auth Sock",
			cwdPath:  "/Users/user/project",
			modPath:  "/Users/user/.ssh/auth.sock",
			expected: "../.ssh/auth.sock",
		},
		{
			name:     "Auth Sock",
			cwdPath:  "/Users/user/project",
			modPath:  "/Users/user/./.1password/agent.sock",
			expected: "../.1password/agent.sock",
		},
		{
			name:     "Windows style paths",
			cwdPath:  `C:\Users\user`,
			modPath:  `C:\Users\user\project`,
			expected: "project",
		},
		{
			name:    "Windows different drives",
			cwdPath: `C:\Users\user`,
			modPath: `D:\Projects\myproject`,
			wantErr: true,
		},
		{
			name:     "Windows UNC paths",
			cwdPath:  `\\server\share\folder`,
			modPath:  `\\server\share\folder\project`,
			expected: "project",
		},
		{
			name:     "Mixed slashes",
			cwdPath:  `/home/user/folder`,
			modPath:  `/home/user/folder/subfolder\project`,
			expected: "subfolder/project",
		},
		{
			name:    "Invalid relative path",
			cwdPath: "/home/user",
			modPath: "C:/Windows",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := LexicalRelativePath(tt.cwdPath, tt.modPath)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestSandboxedRelativePath(t *testing.T) {
	tests := []struct {
		name     string
		userPath string
		root     string
		expected string
		wantErr  bool
	}{
		{
			name:     "simple relative path",
			userPath: "src",
			root:     "/home/user/project",
			expected: "/home/user/project/src",
		},
		{
			name:     "dot path returns root",
			userPath: ".",
			root:     "/home/user/project",
			expected: "/home/user/project",
		},
		{
			name:     "nested relative path",
			userPath: "a/b/c",
			root:     "/home/user/project",
			expected: "/home/user/project/a/b/c",
		},
		{
			name:     "absolute path treated as relative",
			userPath: "/sub/dir",
			root:     "/home/user/project",
			expected: "/home/user/project/sub/dir",
		},
		{
			name:     "dotdot within bounds",
			userPath: "a/b/../c",
			root:     "/home/user/project",
			expected: "/home/user/project/a/c",
		},
		{
			name:     "dotdot escapes root",
			userPath: "../..",
			root:     "/home/user/project",
			wantErr:  true,
		},
		{
			name:     "sneaky dotdot escape",
			userPath: "a/../../..",
			root:     "/home/user/project",
			wantErr:  true,
		},
		{
			name:     "Windows backslash path",
			userPath: `sub\dir\file`,
			root:     "/home/user/project",
			expected: "/home/user/project/sub/dir/file",
		},
		{
			name:     "Windows drive letter stripped",
			userPath: `C:\Users\foo`,
			root:     "/work",
			expected: "/work/Users/foo",
		},
		{
			name:     "Windows root with drive letter",
			userPath: "src",
			root:     "C:/Users/foo/project",
			expected: "C:/Users/foo/project/src",
		},
		{
			name:     "Windows root dotdot escape",
			userPath: "../..",
			root:     "C:/Users/foo/project",
			wantErr:  true,
		},
		{
			name:     "empty userPath returns root",
			userPath: "",
			root:     "/home/user/project",
			expected: "/home/user/project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SandboxedRelativePath(tt.userPath, tt.root)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}
