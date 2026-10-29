package server

import (
	"os"
	"sync"
)

// readFilesAhead reads each file once, start to end, all files in parallel,
// and returns when every read ends. It is for the local state databases: when
// the bolt databases open and the dagql cache imports, they read one page at a
// time in tree order, which costs one random read per page while the page
// cache is cold, as on a newly attached engine volume. After one sequential
// pass those pages come from the page cache. A file that is missing or fails
// to read is skipped; the database that opens it reports any error.
func readFilesAhead(paths ...string) {
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Go(func() {
			readFileAhead(path)
		})
	}
	wg.Wait()
}

func readFileAhead(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	for {
		if _, err := f.Read(buf); err != nil {
			return
		}
	}
}
