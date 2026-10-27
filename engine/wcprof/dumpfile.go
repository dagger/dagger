package wcprof

import (
	"compress/gzip"
	"os"
	"path/filepath"
)

// StopDumpPath is where an engine with state directory root keeps the dump
// of its stop (see WriteDumpFile).
func StopDumpPath(root string) string {
	return filepath.Join(root, "wcprof", "stop.ndjson.gz")
}

// WriteDumpFile writes the recorder's dump to path, gzipped, without flushing
// the recorder. It writes a temporary file beside path and renames it over
// path, so a reader, or an engine killed mid-write, never leaves a partial
// dump. It does nothing when profiling was never enabled.
//
// The engine writes its stop dump this way when it receives SIGTERM, as each
// graceful-stop phase begins, and once the stop ends: an engine killed before
// it exits leaves the dump of its last finished write, with the work still
// running then as open ops. It compresses at gzip's fastest level, since it
// runs inside the stop it records.
func WriteDumpFile(path string) (err error) {
	r := Active()
	if r == nil {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	zw, err := gzip.NewWriterLevel(tmp, gzip.BestSpeed)
	if err != nil {
		return err
	}
	if err := r.WriteDump(zw, false); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
