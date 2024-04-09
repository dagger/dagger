package cacerts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

/* TODO: open questions
* Alpine has both /etc/ssl/ and /etc/ssl1.1 dirs...
* LibreSSL does it's own thing? https://wiki.archlinux.org/title/Transport_Layer_Security
* GNUTLS too; uses pkcs11 stuff (can other things be custom compiled to use that?)
* More variations here: https://go.dev/src/crypto/x509/root_linux.go

More distros to handle:
* Arch Linux
* OpenSUSE/sles
* NiXOS
* BusyBox
* Wolfi
*/

/*
This is anything that uses:
* bundle path: /etc/ssl/certs/ca-certificates.crt
* custom CA dir: /usr/local/share/ca-certificates
* update command: update-ca-certificates

This is known to include:
* Debian/Ubuntu/other derivatives
* Alpine
* Gentoo

Which are obviously not all Debian derivatives...
It's named debianLike for lack of a better name :-)
*/
type debianLike struct {
	ctrFS *containerFS

	bundleExisted          bool
	customCACertDirExisted bool
	updateCommandExisted   bool

	existingCerts  map[string]string
	installedCerts map[string]string

	existingBundledCerts map[string]struct{}

	existingSymlinks  map[string]string
	installedSymlinks map[string]string
}

func (debianLike) bundlePath() string {
	return "/etc/ssl/certs/ca-certificates.crt"
}

func (debianLike) customCACertDir() string {
	return "/usr/local/share/ca-certificates"
}

func (d *debianLike) detect(ctx context.Context) (bool, error) {
	if exists, err := d.ctrFS.AnyPathExists([]string{
		"/etc/debian_version",
		"/etc/alpine-release",
		"/etc/gentoo-release",
	}); err != nil {
		return false, err
	} else if exists {
		return true, nil
	}

	return d.ctrFS.OSReleaseFileContains(
		[][]byte{
			[]byte("debian"),
			[]byte("ubuntu"),
			[]byte("alpine"),
			[]byte("gentoo"),
		},
		[][]byte{
			[]byte("debian"),
			[]byte("ubuntu"),
			[]byte("alpine"),
			[]byte("gentoo"),
		},
	)
}

func (d *debianLike) Install(ctx context.Context) error {
	var err error
	d.bundleExisted, err = d.ctrFS.PathExists(d.bundlePath())
	if err != nil {
		return err
	}

	d.customCACertDirExisted, err = d.ctrFS.PathExists(d.customCACertDir())
	if err != nil {
		return err
	}

	updateCmdPath, lookupErr := d.ctrFS.LookPath("update-ca-certificates")
	d.updateCommandExisted = lookupErr == nil
	if !d.updateCommandExisted && !errors.Is(lookupErr, exec.ErrNotFound) {
		return fmt.Errorf("failed to lookup update-ca-certificates: %w", lookupErr)
	}

	d.installedCerts, d.installedSymlinks, err = ReadHostCustomCADir(EngineCustomCACertsDir)
	if err != nil {
		return fmt.Errorf("failed to read custom CA dir: %w", err)
	}

	if d.bundleExisted {
		d.existingBundledCerts, err = d.ctrFS.ReadCABundleFile(d.bundlePath())
		if err != nil {
			return fmt.Errorf("failed to read existing bundle: %w", err)
		}
	}

	if d.customCACertDirExisted {
		d.existingCerts, d.existingSymlinks, err = d.ctrFS.ReadCustomCADir(d.customCACertDir())
		if err != nil {
			return fmt.Errorf("failed to read existing custom CA dir: %w", err)
		}
	} else {
		// TODO: track what parent dirs we create so we can clean up fully
		// TODO: double check perms here
		if err := d.ctrFS.MkdirAll(d.customCACertDir(), 0755); err != nil {
			return err
		}
	}

	// install to custom CA dir even when command doesn't exist to handle case where the exec installs ca-certificates
	// TODO: parallelize symlink+file install
	for installSymlink, target := range d.installedSymlinks {
		destPath := filepath.Join(d.customCACertDir(), installSymlink)
		if _, err := d.ctrFS.Lstat(destPath); err == nil {
			// already exists, skip
			delete(d.installedSymlinks, installSymlink)
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := d.ctrFS.Symlink(target, destPath); err != nil {
			return err
		}
	}
	for certContents, certFileName := range d.installedCerts {
		destPath := filepath.Join(d.customCACertDir(), certFileName)
		if _, err := d.ctrFS.Lstat(destPath); err == nil {
			// already exists, skip
			delete(d.installedCerts, certContents)
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := d.ctrFS.WriteFile(destPath, []byte(certContents+"\n"), 0644); err != nil {
			return err
		}
	}

	if d.updateCommandExisted {
		if err := d.ctrFS.Exec(ctx, updateCmdPath); err != nil {
			return fmt.Errorf("failed to run update-ca-certificates for install: %w", err)
		}
		return nil
	}

	if !d.bundleExisted {
		// TODO: track what parent dirs we create so we can clean up fully
		// TODO: double check perms here
		if err := d.ctrFS.MkdirAll(filepath.Dir(d.bundlePath()), 0755); err != nil {
			return err
		}
	}

	f, err := d.ctrFS.OpenFile(d.bundlePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	for installCert := range d.installedCerts {
		// skip installing certs that are already in the bundle
		if _, exists := d.existingBundledCerts[installCert]; exists {
			delete(d.installedCerts, installCert)
			continue
		}
		if _, err := f.WriteString(installCert + "\n\n"); err != nil {
			return err
		}
	}
	return nil
}

func (d *debianLike) Uninstall(ctx context.Context) error {
	// TODO: parallelize symlink+file uninstall
	for installSymlink := range d.installedSymlinks {
		destPath := filepath.Join(d.customCACertDir(), installSymlink)
		// best effort, if it didn't exist because the exec deleted it, that's fine
		d.ctrFS.Remove(destPath)
	}
	// TODO: it's *technically* possible that the exec overwrote a file here, in which case
	// we don't want to delete it. Can use create time
	for _, certFileName := range d.installedCerts {
		destPath := filepath.Join(d.customCACertDir(), certFileName)
		// best effort, if it didn't exist because the exec deleted it, that's fine
		d.ctrFS.Remove(destPath)
	}

	// The update command could have existed before but got uninstalled, or it could have not existed
	// before and got installed by the exec. Either way, need to check for it again now.
	updateCmdPath, lookupErr := d.ctrFS.LookPath("update-ca-certificates")
	updateCommandExists := lookupErr == nil
	if !updateCommandExists && !errors.Is(lookupErr, exec.ErrNotFound) {
		return fmt.Errorf("failed to lookup update-ca-certificates: %w", lookupErr)
	}

	// update the bundle using the command if available, otherwise manually remove the certs
	if updateCommandExists {
		if err := d.ctrFS.Exec(ctx, updateCmdPath); err != nil {
			return fmt.Errorf("failed to run update-ca-certificates for uninstall: %w", err)
		}
	} else if err := d.ctrFS.RemoveCertsFromCABundle(d.bundlePath(), d.installedCerts); err != nil {
		return err
	}

	if !d.bundleExisted {
		// if the bundle didn't exist before, remove it provided it's now empty after removing our installed certs
		stat, err := d.ctrFS.Stat(d.bundlePath())
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if stat != nil && stat.Size() == 0 {
			if err := d.ctrFS.Remove(d.bundlePath()); err != nil {
				return err
			}
		}
	}

	// only remove the custom CA dir if it didn't exist before and it expected to have been created or removed
	// by the exec. We heuristically determine this by checking the before/after state of the update command, which
	// tells us whether the dir is expected to exist or not.
	cmdNeverExisted := !d.updateCommandExisted && !updateCommandExists
	cmdWasRemoved := d.updateCommandExisted && !updateCommandExists
	if (!d.customCACertDirExisted && cmdNeverExisted) || (d.customCACertDirExisted && cmdWasRemoved) {
		// if the custom CA dir didn't exist before, remove it provided it's now empty after removing our installed certs
		isEmpty, err := d.ctrFS.DirIsEmpty(d.customCACertDir())
		if err != nil {
			return err
		}
		if isEmpty {
			if err := d.ctrFS.Remove(d.customCACertDir()); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}

	return nil
}

/*
RHEL and derivatives use:
* bundle path: /etc/pki/tls/certs/ca-bundle.crt
* custom CA dir: /etc/pki/ca-trust/source/anchors
* update command: trust extract

This is known to include:
* RHEL
* Fedora
* CentOS
* Amazon Linux
*/
type rhelLike struct {
	ctrFS *containerFS

	bundleExisted          bool
	customCACertDirExisted bool
	updateCommandExisted   bool

	existingCerts  map[string]string
	installedCerts map[string]string

	existingBundledCerts map[string]struct{}

	existingSymlinks  map[string]string
	installedSymlinks map[string]string
}

func (rhelLike) bundlePath() string {
	return "/etc/pki/tls/certs/ca-bundle.crt"
}

func (rhelLike) customCACertDir() string {
	return "/etc/pki/ca-trust/source/anchors"
}

func (rhelLike) updateCmd() []string {
	return []string{"update-ca-trust"}
}

func (d *rhelLike) detect(ctx context.Context) (bool, error) {
	if exists, err := d.ctrFS.AnyPathExists([]string{
		"/etc/redhat-release",
		"/etc/redhat-version",
		"/etc/centos-release",
	}); err != nil {
		return false, err
	} else if exists {
		return true, nil
	}

	return d.ctrFS.OSReleaseFileContains(
		[][]byte{
			[]byte("rhel"),
			[]byte("fedora"),
			[]byte("centos"),
			[]byte("amzn"),
		},
		[][]byte{
			[]byte("rhel"),
			[]byte("centos"),
			[]byte("fedora"),
		},
	)
}

func (d *rhelLike) Install(ctx context.Context) error {
	var err error
	d.bundleExisted, err = d.ctrFS.PathExists(d.bundlePath())
	if err != nil {
		return err
	}

	d.customCACertDirExisted, err = d.ctrFS.PathExists(d.customCACertDir())
	if err != nil {
		return err
	}

	_, lookupErr := d.ctrFS.LookPath(d.updateCmd()[0])
	d.updateCommandExisted = lookupErr == nil
	if !d.updateCommandExisted && !errors.Is(lookupErr, exec.ErrNotFound) {
		return fmt.Errorf("failed to lookup %s: %w", d.updateCmd()[0], lookupErr)
	}

	d.installedCerts, d.installedSymlinks, err = ReadHostCustomCADir(EngineCustomCACertsDir)
	if err != nil {
		return fmt.Errorf("failed to read custom CA dir: %w", err)
	}

	if d.bundleExisted {
		d.existingBundledCerts, err = d.ctrFS.ReadCABundleFile(d.bundlePath())
		if err != nil {
			return fmt.Errorf("failed to read existing bundle: %w", err)
		}
	}

	if d.customCACertDirExisted {
		d.existingCerts, d.existingSymlinks, err = d.ctrFS.ReadCustomCADir(d.customCACertDir())
		if err != nil {
			return fmt.Errorf("failed to read existing custom CA dir: %w", err)
		}
	} else {
		// TODO: track what parent dirs we create so we can clean up fully
		// TODO: double check perms here
		if err := d.ctrFS.MkdirAll(d.customCACertDir(), 0755); err != nil {
			return err
		}
	}

	// install to custom CA dir even when command doesn't exist to handle case where the exec installs ca-certificates
	// TODO: parallelize symlink+file install
	for installSymlink, target := range d.installedSymlinks {
		destPath := filepath.Join(d.customCACertDir(), installSymlink)
		if _, err := d.ctrFS.Lstat(destPath); err == nil {
			// already exists, skip
			delete(d.installedSymlinks, installSymlink)
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := d.ctrFS.Symlink(target, destPath); err != nil {
			return err
		}
	}
	for certContents, certFileName := range d.installedCerts {
		destPath := filepath.Join(d.customCACertDir(), certFileName)
		if _, err := d.ctrFS.Lstat(destPath); err == nil {
			// already exists, skip
			delete(d.installedCerts, certContents)
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := d.ctrFS.WriteFile(destPath, []byte(certContents+"\n"), 0644); err != nil {
			return err
		}
	}

	if d.updateCommandExisted {
		if err := d.ctrFS.Exec(ctx, d.updateCmd()...); err != nil {
			return fmt.Errorf("failed to run %v for install: %w", d.updateCmd(), err)
		}
		return nil
	}

	if !d.bundleExisted {
		// TODO: track what parent dirs we create so we can clean up fully
		// TODO: double check perms here
		if err := d.ctrFS.MkdirAll(filepath.Dir(d.bundlePath()), 0755); err != nil {
			return err
		}
	}

	f, err := d.ctrFS.OpenFile(d.bundlePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	for installCert := range d.installedCerts {
		// skip installing certs that are already in the bundle
		if _, exists := d.existingBundledCerts[installCert]; exists {
			delete(d.installedCerts, installCert)
			continue
		}
		if _, err := f.WriteString(installCert + "\n\n"); err != nil {
			return err
		}
	}
	return nil
}

func (d *rhelLike) Uninstall(ctx context.Context) error {
	// TODO: parallelize symlink+file uninstall
	for installSymlink := range d.installedSymlinks {
		destPath := filepath.Join(d.customCACertDir(), installSymlink)
		// best effort, if it didn't exist because the exec deleted it, that's fine
		d.ctrFS.Remove(destPath)
	}
	// TODO: it's *technically* possible that the exec overwrote a file here, in which case
	// we don't want to delete it. Can use create time
	for _, certFileName := range d.installedCerts {
		destPath := filepath.Join(d.customCACertDir(), certFileName)
		// best effort, if it didn't exist because the exec deleted it, that's fine
		d.ctrFS.Remove(destPath)
	}

	// The update command could have existed before but got uninstalled, or it could have not existed
	// before and got installed by the exec. Either way, need to check for it again now.
	_, lookupErr := d.ctrFS.LookPath(d.updateCmd()[0])
	updateCommandExists := lookupErr == nil
	if !updateCommandExists && !errors.Is(lookupErr, exec.ErrNotFound) {
		return fmt.Errorf("failed to lookup %s: %w", d.updateCmd()[0], lookupErr)
	}

	// update the bundle using the command if available, otherwise manually remove the certs
	if updateCommandExists {
		if err := d.ctrFS.Exec(ctx, d.updateCmd()...); err != nil {
			return fmt.Errorf("failed to run %v for install: %w", d.updateCmd(), err)
		}
	} else if err := d.ctrFS.RemoveCertsFromCABundle(d.bundlePath(), d.installedCerts); err != nil {
		return err
	}

	if !d.bundleExisted {
		// if the bundle didn't exist before, remove it provided it's now empty after removing our installed certs
		stat, err := d.ctrFS.Stat(d.bundlePath())
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if stat != nil && stat.Size() == 0 {
			if err := d.ctrFS.Remove(d.bundlePath()); err != nil {
				return err
			}
		}
	}

	// only remove the custom CA dir if it didn't exist before and it expected to have been created or removed
	// by the exec. We heuristically determine this by checking the before/after state of the update command, which
	// tells us whether the dir is expected to exist or not.
	cmdNeverExisted := !d.updateCommandExisted && !updateCommandExists
	cmdWasRemoved := d.updateCommandExisted && !updateCommandExists
	if (!d.customCACertDirExisted && cmdNeverExisted) || (d.customCACertDirExisted && cmdWasRemoved) {
		// if the custom CA dir didn't exist before, remove it provided it's now empty after removing our installed certs
		isEmpty, err := d.ctrFS.DirIsEmpty(d.customCACertDir())
		if err != nil {
			return err
		}
		if isEmpty {
			if err := d.ctrFS.Remove(d.customCACertDir()); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}

	return nil
}
