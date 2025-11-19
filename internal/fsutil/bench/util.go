package bench

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

func createTestDir(n int) (string, error) {
	const nesting = 1.0 / 3.0
	rootDir, err := os.MkdirTemp(os.Getenv("BENCH_BASE_DIR"), "diffcopy")
	if err != nil {
		return "", err
	}

	dirs := int(math.Ceil(math.Pow(float64(n), nesting)))
	if err := fillTestDir(rootDir, dirs, n); err != nil {
		os.RemoveAll(rootDir)
		return "", err
	}
	return rootDir, nil
}

func fillTestDir(root string, items, n int) error {
	if n <= items {
		for i := 0; i < items; i++ {
			fp := filepath.Join(root, randomID())
			if err := writeFile(fp); err != nil {
				return err
			}
		}
	} else {
		sub := n / items
		for n > 0 {
			fp := filepath.Join(root, randomID())
			if err := os.MkdirAll(fp, 0700); err != nil {
				return err
			}
			if n < sub {
				sub = n
			}
			if err := fillTestDir(fp, items, sub); err != nil {
				return err
			}
			n -= sub
		}
	}
	return nil
}

func randomID() string {
	b := make([]byte, 10)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var buf []byte
var once sync.Once

func randBuf() []byte {
	once.Do(func() {
		var size int64 = 64 * 1024
		if s, err := strconv.ParseInt(os.Getenv("BENCH_FILE_SIZE"), 10, 64); err == nil {
			size = s
		}
		buf = make([]byte, size)
		rand.Read(buf)
	})
	return buf
}
func writeFile(p string) error {
	tf, err := os.Create(p)
	if err != nil {
		return err
	}
	if _, err := tf.Write(randBuf()); err != nil {
		return err
	}
	return tf.Close()
}

func mutate(root string, n int) error {
	del := n
	add := n
	mod := n
	stop := errors.New("")

	for {
		if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if !info.IsDir() {
				if mathrand.Intn(3) == 0 {
					switch mathrand.Intn(3) {
					case 0:
						if del > 0 {
							del--
							os.RemoveAll(path)
						}
					case 1:
						if add > 0 {
							add--
							fp := filepath.Join(filepath.Dir(path), randomID())
							if err := writeFile(fp); err != nil {
								return err
							}
						}
					case 2:
						if mod > 0 {
							mod--
							if err := writeFile(path); err != nil {
								return err
							}
						}
					}
				}
			}
			if add+mod+del == 0 {
				return stop
			}
			return nil
		}); err != nil {
			if err == stop {
				return nil
			}
			return err
		}
	}
}
