package containerfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadHostCustomCADirK8sSymlinks(t *testing.T) {
	// Simulate the Kubernetes Secret mount structure:
	//   ca.pem -> ..data/ca.pem
	//   ..data -> ..2025_01_01_00_00_00.123456789
	//   ..2025_01_01_00_00_00.123456789/ca.pem  (actual file)
	dir := t.TempDir()

	// Create the timestamped directory with actual cert files
	tsDir := filepath.Join(dir, "..2025_01_01_00_00_00.123456789")
	if err := os.Mkdir(tsDir, 0755); err != nil {
		t.Fatal(err)
	}
	certContents := "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIJAL...\n-----END CERTIFICATE-----"
	if err := os.WriteFile(filepath.Join(tsDir, "ca.pem"), []byte(certContents), 0644); err != nil {
		t.Fatal(err)
	}
	otherCert := "-----BEGIN CERTIFICATE-----\nABCDEF...\n-----END CERTIFICATE-----"
	if err := os.WriteFile(filepath.Join(tsDir, "extra.pem"), []byte(otherCert), 0644); err != nil {
		t.Fatal(err)
	}

	// Create ..data symlink pointing to timestamped dir
	if err := os.Symlink("..2025_01_01_00_00_00.123456789", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}

	// Create cert symlinks pointing through ..data
	if err := os.Symlink("..data/ca.pem", filepath.Join(dir, "ca.pem")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..data/extra.pem", filepath.Join(dir, "extra.pem")); err != nil {
		t.Fatal(err)
	}

	certs, symlinks, err := ReadHostCustomCADir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Symlinks should be empty -- certs are resolved to file contents
	if len(symlinks) != 0 {
		t.Errorf("expected no symlinks, got %d: %v", len(symlinks), symlinks)
	}

	// Both certs should be in the certs map
	if len(certs) != 2 {
		t.Errorf("expected 2 certs, got %d: %v", len(certs), certs)
	}
	if name, ok := certs[certContents]; !ok || name != "ca.pem" {
		t.Errorf("expected ca.pem cert, got name=%q ok=%v", name, ok)
	}
	if name, ok := certs[otherCert]; !ok || name != "extra.pem" {
		t.Errorf("expected extra.pem cert, got name=%q ok=%v", name, ok)
	}
}

func TestReadHostCustomCADirRegularFiles(t *testing.T) {
	dir := t.TempDir()

	cert := "-----BEGIN CERTIFICATE-----\nREGULAR...\n-----END CERTIFICATE-----"
	if err := os.WriteFile(filepath.Join(dir, "my-cert.pem"), []byte(cert), 0644); err != nil {
		t.Fatal(err)
	}

	certs, symlinks, err := ReadHostCustomCADir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(symlinks) != 0 {
		t.Errorf("expected no symlinks, got %d", len(symlinks))
	}
	if len(certs) != 1 {
		t.Errorf("expected 1 cert, got %d", len(certs))
	}
	if name, ok := certs[cert]; !ok || name != "my-cert.pem" {
		t.Errorf("expected my-cert.pem, got name=%q ok=%v", name, ok)
	}
}

func TestReadHostCustomCADirNonexistent(t *testing.T) {
	certs, symlinks, err := ReadHostCustomCADir("/nonexistent/path")
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 0 || len(symlinks) != 0 {
		t.Errorf("expected empty results for nonexistent dir")
	}
}
