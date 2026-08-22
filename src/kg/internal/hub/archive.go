package hub

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// canonicalDBName is the normalized name every pushed database gets inside
// the archive (and thus inside the hub's storage), regardless of the scope
// database's original filename.
const canonicalDBName = "knowledge.db"

// maxExtractBytes caps the total bytes UnpackDB will write (16 GiB).
const maxExtractBytes = int64(16) << 30

// PackDB writes a gzip'd tar archive of the Kuzu database at dbPath to w.
// Kuzu databases are a single file, possibly with a sibling WAL
// (<db>.wal); older Kuzu versions used a directory. Both are handled.
// Entry names are normalized: the database itself becomes "knowledge.db"
// (a directory database becomes entries under "knowledge.db/"), and a
// sibling WAL file becomes "knowledge.db.wal". Symlinks are not followed.
func PackDB(w io.Writer, dbPath string) error {
	info, err := os.Lstat(dbPath)
	if err != nil {
		return fmt.Errorf("stat database: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	if info.IsDir() {
		err = packDir(tw, dbPath)
	} else if info.Mode().IsRegular() {
		err = packFile(tw, dbPath, canonicalDBName, info)
	} else {
		err = fmt.Errorf("database %s is neither a regular file nor a directory", dbPath)
	}
	if err != nil {
		return err
	}

	// Sibling WAL file, if present.
	walPath := dbPath + ".wal"
	if walInfo, werr := os.Lstat(walPath); werr == nil && walInfo.Mode().IsRegular() {
		if err := packFile(tw, walPath, canonicalDBName+".wal", walInfo); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("finalize tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("finalize gzip: %w", err)
	}
	return nil
}

func packDir(tw *tar.Writer, dbPath string) error {
	return filepath.Walk(dbPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dbPath, path)
		if err != nil {
			return err
		}
		name := canonicalDBName
		if rel != "." {
			name = canonicalDBName + "/" + filepath.ToSlash(rel)
		}
		switch {
		case info.IsDir():
			hdr := &tar.Header{
				Name:     name + "/",
				Typeflag: tar.TypeDir,
				Mode:     int64(info.Mode().Perm()),
				ModTime:  info.ModTime(),
			}
			return tw.WriteHeader(hdr)
		case info.Mode().IsRegular():
			return packFile(tw, path, name, info)
		default:
			// Skip symlinks and other special files.
			return nil
		}
	})
}

func packFile(tw *tar.Writer, path, name string, info os.FileInfo) error {
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     info.Size(),
		Mode:     int64(info.Mode().Perm()),
		ModTime:  info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("archive %s: %w", path, err)
	}
	return nil
}

// UnpackDB extracts a PackDB archive from r into destDir, producing
// destDir/knowledge.db (plus knowledge.db.wal when present). It rejects
// path-traversal entries (absolute names, or any ".." component after
// cleaning), symlink/hardlink/device entries, and caps the total extracted
// size at 16 GiB.
func UnpackDB(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		name, err := safeEntryName(hdr.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(destDir, name)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode).Perm()|0700); err != nil {
				return fmt.Errorf("create directory %s: %w", name, err)
			}
		case tar.TypeReg:
			remaining := maxExtractBytes - total
			if remaining <= 0 || hdr.Size > remaining {
				return fmt.Errorf("archive exceeds %d byte extraction limit", maxExtractBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("create parent of %s: %w", name, err)
			}
			n, err := extractFile(tr, target, os.FileMode(hdr.Mode).Perm(), remaining)
			total += n
			if err != nil {
				return fmt.Errorf("extract %s: %w", name, err)
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type %d (symlinks, hardlinks, and devices are rejected)", hdr.Name, hdr.Typeflag)
		}
	}
}

// safeEntryName validates and cleans a tar entry name, rejecting anything
// that could escape the extraction directory.
func safeEntryName(name string) (string, error) {
	if strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("archive entry %q has an absolute path", name)
	}
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) ||
		strings.Contains(cleaned, string(filepath.Separator)+".."+string(filepath.Separator)) ||
		strings.HasSuffix(cleaned, string(filepath.Separator)+"..") {
		return "", fmt.Errorf("archive entry %q escapes the extraction directory", name)
	}
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("archive entry %q has an absolute path", name)
	}
	return cleaned, nil
}

func extractFile(r io.Reader, target string, mode os.FileMode, limit int64) (int64, error) {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0600)
	if err != nil {
		return 0, err
	}
	// Read one byte past the limit so overruns are detected rather than
	// silently truncated.
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, fmt.Errorf("archive exceeds %d byte extraction limit", maxExtractBytes)
	}
	return n, nil
}
