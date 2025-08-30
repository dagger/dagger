package fsxutil

import (
	"bytes"
	"fmt"
	gofs "io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
	"github.com/tonistiigi/fsutil/types"
)

// bufWalkDir is a helper function that matches the style in filter_test.go
func bufWalkDir(buf *bytes.Buffer) gofs.WalkDirFunc {
	return func(path string, entry gofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := entry.Info()
		if err != nil {
			return err
		}

		t := "file"
		if fi.IsDir() {
			t = "dir"
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			t = "symlink"
		}
		fmt.Fprintf(buf, "%s %s\n", t, path)
		return nil
	}
}

func tmpDir(inp []*change) (dir string, retErr error) {
	tmpdir, err := os.MkdirTemp("", "diff")
	if err != nil {
		return "", err
	}
	defer func() {
		if retErr != nil {
			os.RemoveAll(tmpdir)
		}
	}()
	for _, c := range inp {
		if c.kind == fsutil.ChangeKindAdd {
			p := filepath.Join(tmpdir, c.path)
			stat, ok := c.fi.Sys().(*types.Stat)
			if !ok {
				return "", errors.Errorf("invalid symlink change %s", p)
			}
			if c.fi.IsDir() {
				if err := os.Mkdir(p, 0700); err != nil {
					return "", err
				}
			} else if c.fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Symlink(stat.Linkname, p); err != nil {
					return "", err
				}
			} else if len(stat.Linkname) > 0 {
				if err := os.Link(filepath.Join(tmpdir, stat.Linkname), p); err != nil {
					return "", err
				}
			} else if c.fi.Mode()&os.ModeSocket != 0 {
				// not closing listener because it would remove the socket file
				if _, err := net.Listen("unix", p); err != nil {
					return "", err
				}
			} else {
				f, err := os.Create(p)
				if err != nil {
					return "", err
				}

				// Make sure all files start with the same default permissions,
				// regardless of OS settings.
				err = os.Chmod(p, 0644)
				if err != nil {
					return "", err
				}

				if len(c.data) > 0 {
					if _, err := f.Write([]byte(c.data)); err != nil {
						return "", err
					}
				}
				f.Close()
			}
		}
	}
	return tmpdir, nil
}

type change struct {
	kind fsutil.ChangeKind
	path string
	fi   os.FileInfo
	data string
}

func changeStream(dt []string) (changes []*change) {
	for _, s := range dt {
		changes = append(changes, parseChange(s))
	}
	return
}

func parseChange(str string) *change {
	errStr := fmt.Sprintf("invalid change %q", str)

	f := splitFields(str)
	if len(f) < 3 {
		panic(errStr)
	}

	c := &change{}
	switch f[0] {
	case "ADD":
		c.kind = fsutil.ChangeKindAdd
	case "CHG":
		c.kind = fsutil.ChangeKindModify
	case "DEL":
		c.kind = fsutil.ChangeKindDelete
	default:
		panic(errStr)
	}
	c.path = filepath.FromSlash(f[1])
	st := &types.Stat{}
	switch f[2] {
	case "file":
		if len(f) > 3 {
			if f[3][0] == '>' {
				st.Linkname = f[3][1:]
			} else {
				c.data = f[3]
			}
		}
	case "dir":
		st.Mode |= uint32(os.ModeDir)
	case "socket":
		st.Mode |= uint32(os.ModeSocket)
	case "symlink":
		if len(f) < 4 {
			panic(errStr)
		}
		st.Mode |= uint32(os.ModeSymlink)
		st.Linkname = f[3]
	}
	c.fi = &fsutil.StatInfo{Stat: st}
	return c
}

func splitFields(s string) []string {
	// Split the string by spaces, but handle quoted strings correctly
	var fields []string
	var current strings.Builder
	inQuotes := false
	escapeNext := false

	for _, r := range s {
		if escapeNext {
			switch r {
			case 'n':
				current.WriteRune('\n')
			case 't':
				current.WriteRune('\t')
			default:
				current.WriteRune(r)
			}
			escapeNext = false
			continue
		}
		if r == '"' {
			inQuotes = !inQuotes
			continue
		}
		if r == '\\' {
			escapeNext = true
			continue
		}
		if r == ' ' && !inQuotes {
			fields = append(fields, current.String())
			current.Reset()
		} else {
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}

	return fields
}
