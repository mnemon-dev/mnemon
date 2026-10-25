package aml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// An instance's private runtime files: the account's provider key file
// (byok.go, ruling R98) and the settings files of the caps that can change at
// run time (ruling R99). Each is a small JSON file of the service user's own,
// mode 0600, which the server reads at its start, refusing one that breaks a
// rule, and writes atomically: a temporary file in the same directory, fsync,
// rename, then the directory's fsync. A crash leaves the old file or the new
// one, never part of one. Errors name the file and the rule it breaks, never
// what it holds.

// maxPrivateFile bounds a private file's size: 64 KiB.
const maxPrivateFile = 64 << 10

// posixFileModes says whether a file's permission bits and owner say who may
// read it, as POSIX systems' do (ruling R89). On Windows they do not, and
// neither is checked there. Tests set it.
var posixFileModes = runtime.GOOS != "windows"

// ownerOf is the uid that owns the file info describes and the process's
// effective uid (fileOwner); ok is false where files have no POSIX owner.
// Tests replace it.
var ownerOf = fileOwner

// writeInterrupted, when a test sets it and it reports true for the file
// being written, stops writePrivateFile between its temporary file and the
// rename, as a crash would there: the temporary file is left behind, and
// the file is untouched.
var writeInterrupted func(path string) bool

// errWriteInterrupted is the error of a write writeInterrupted stopped.
var errWriteInterrupted = errors.New("the write stopped before its rename")

// checkPrivate refuses, naming what the file is (what) and its path, a file
// that is not a regular one (a directory, a symbolic link), or, where POSIX
// modes mean something, one that its group or others may read or write, or
// that another user than the process's owns.
func checkPrivate(what, path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s %s is not a regular file", what, path)
	}
	if !posixFileModes {
		return nil
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s %s has mode %04o: its group or others may read or write it; chmod 600 it", what, path, perm)
	}
	if owner, process, ok := ownerOf(info); ok && owner != process {
		return fmt.Errorf("%s %s is owned by uid %d, not by this process's uid %d", what, path, owner, process)
	}
	return nil
}

// readPrivateFile reads the private file at path, what saying what it is in
// an error: its content, and whether it exists (a missing file is no error).
// A file that checkPrivate refuses, or that is larger than maxPrivateFile,
// is an error. The file is opened by the name it was checked by and checked
// to be the one opened.
func readPrivateFile(what, path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s %s cannot be read: %w", what, path, reason(err))
	}
	if err := checkPrivate(what, path, info); err != nil {
		return nil, true, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, true, fmt.Errorf("%s %s cannot be read: %w", what, path, reason(err))
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, true, fmt.Errorf("%s %s changed while it was read", what, path)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxPrivateFile+1))
	if err != nil {
		return nil, true, fmt.Errorf("%s %s cannot be read: %w", what, path, reason(err))
	}
	if len(raw) > maxPrivateFile {
		return nil, true, fmt.Errorf("%s %s is larger than %d KiB", what, path, maxPrivateFile>>10)
	}
	return raw, true, nil
}

// reason is err without the path a *fs.PathError repeats.
func reason(err error) error {
	var pathError *fs.PathError
	if errors.As(err, &pathError) {
		return pathError.Err
	}
	return err
}

// decodeStrict decodes the one JSON value of raw into into: a field into
// does not have, or anything but white space after the value, is an error.
// The error says nothing of raw, whose JSON errors may quote what it holds.
func decodeStrict(raw []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || readEnd(decoder) != nil {
		return errors.New("not the file's format")
	}
	return nil
}

// tempPrefix is the name prefix of the temporary files written beside path.
func tempPrefix(path string) string { return "." + filepath.Base(path) + ".tmp-" }

// writePrivateFile writes data to path atomically, mode 0600: a temporary
// file in its directory (tempPrefix), fsync, rename over path, then the
// directory's fsync. A failed write removes its temporary file and leaves
// path as it was.
func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, tempPrefix(path)+"*")
	if err != nil {
		return err
	}
	temp, kept := file.Name(), false
	defer func() {
		if !kept {
			os.Remove(temp)
		}
	}()
	if err := file.Chmod(0o600); err != nil && posixFileModes {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if writeInterrupted != nil && writeInterrupted(path) {
		kept = true
		return errWriteInterrupted
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	kept = true
	syncDir(dir)
	return nil
}

// removePrivateFile removes path, a missing file being no error, then
// fsyncs its directory.
func removePrivateFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir fsyncs a directory, so that a rename or a removal in it outlasts a
// crash; at best, since the file system has already done what was asked of
// it: Windows cannot fsync a directory, and a failure changes nothing that
// was done.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// removeInterrupted removes the temporary files that writes of path a crash
// interrupted left beside it, and returns their names. Only the file's one
// writer calls it, at its start.
func removeInterrupted(path string) []string {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return nil
	}
	removed := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), tempPrefix(path)) && entry.Type().IsRegular() {
			if os.Remove(filepath.Join(filepath.Dir(path), entry.Name())) == nil {
				removed = append(removed, entry.Name())
			}
		}
	}
	return removed
}

// checkStateFile checks the path of a private file of the instance's state:
// absolute, and outside the data root, however a path spells it
// (canonicalPath), so that a snapshot of the data root never holds it.
func checkStateFile(what, path, dataRoot string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path", what)
	}
	name, err := canonicalPath(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	root, err := canonicalPath(filepath.Clean(dataRoot))
	if err != nil {
		return fmt.Errorf("the data root: %w", err)
	}
	if rel, err := filepath.Rel(root, name); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must be outside the data root: a snapshot of the data root must never hold it", what)
	}
	return nil
}
