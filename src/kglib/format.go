package kglib

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Storage-format detection.
//
// The migration design called for reacting to "a format-mismatch open error"
// from Kuzu. That error does not exist at this layer: go-kuzu's OpenDatabase
// discards the engine's diagnostic and returns only
//
//	fmt.Errorf("failed to open database with status %d", status)
//
// so a storage-format mismatch, a held lock, a missing file, and a corrupt
// database all arrive as an indistinguishable "status 1". Guessing between them
// from the error text is how the pre-existing open path came to report a
// database that simply does not exist as "locked by another process".
//
// Detection therefore does not depend on the engine's error at all. Every
// database gets a sidecar file naming the engine build that wrote it, and the
// check is a file comparison made *before* the open is attempted. The stamp
// lives outside the database for the obvious reason: a database that will not
// open cannot be asked what wrote it.
//
// The version stamped is the pinned go-kuzu module version rather than the
// engine's own kuzu_get_storage_version(), which go-kuzu exposes no binding
// for. The module version is the thing that actually changes when the storage
// format can change, which is what the check needs to know.
//
// Like the journal, this has to ship before a format-breaking bump: a database
// written before stamping existed has no sidecar, and reports FormatUnstamped
// rather than a verdict. That case is what the installer's retained-binary
// safety net is for.

// FormatStampFile is the sidecar naming the engine build that wrote a database.
const formatStampSuffix = ".format.json"

// FormatStamp is the content of that sidecar.
type FormatStamp struct {
	// KuzuVersion is the pinned go-kuzu module version, e.g. "v0.11.3".
	KuzuVersion string `json:"kuzu_version"`
	// KGVersion is the kg build that wrote the stamp, for diagnostics only.
	KGVersion string    `json:"kg_version,omitempty"`
	StampedAt time.Time `json:"stamped_at"`
}

// FormatStatus is the verdict of comparing a database's stamp to this build.
type FormatStatus int

const (
	// FormatOK means the stamp matches the running engine.
	FormatOK FormatStatus = iota
	// FormatMissing means there is no database at that path.
	FormatMissing
	// FormatUnstamped means a database exists but predates stamping, so
	// compatibility cannot be determined without trying to open it.
	FormatUnstamped
	// FormatMismatch means the database was written by a different engine build
	// and may need rebuilding.
	FormatMismatch
)

func (f FormatStatus) String() string {
	switch f {
	case FormatOK:
		return "ok"
	case FormatMissing:
		return "missing"
	case FormatUnstamped:
		return "unstamped"
	case FormatMismatch:
		return "mismatch"
	default:
		return "unknown"
	}
}

var (
	kuzuVersionOnce sync.Once
	kuzuVersionVal  string
)

const kuzuModulePath = "github.com/kuzudb/go-kuzu"

// goModBytes is this module's own go.mod, embedded so the go-kuzu version
// stays recoverable when build metadata does not carry it. See KuzuVersion.
//
//go:embed go.mod
var goModBytes []byte

// KuzuVersion returns the go-kuzu module version this binary is built against,
// or "unknown" if it cannot be determined at all.
//
// Build info is the accurate source — it reports the version actually selected
// and linked, which module-graph resolution can raise above what go.mod
// requires. But it is not always populated: debug.ReadBuildInfo().Deps is
// empty in *test binaries* under Go toolchains before 1.27 (verified: go1.26.2
// reports len(Deps)==0 in a test binary and 15 in an ordinary one, on the same
// source). Reading the requirement out of the embedded go.mod covers that case
// and any other build mode that drops dependency metadata.
//
// Falling back matters because the failure was silent. This function used to
// return "unknown" whenever the dep was absent from build info, WriteFormatStamp
// then recorded "unknown" as the engine version, and CheckFormat compared
// "unknown" against "unknown" and reported a match — so the mismatch detection
// this whole file exists to provide would quietly pass everything while
// appearing to work. A stamp is only worth writing if the version in it is real.
func KuzuVersion() string {
	kuzuVersionOnce.Do(func() {
		if v := kuzuVersionFromBuildInfo(); v != "" {
			kuzuVersionVal = v
			return
		}
		if v := kuzuVersionFromGoMod(goModBytes); v != "" {
			kuzuVersionVal = v
			return
		}
		kuzuVersionVal = "unknown"
	})
	return kuzuVersionVal
}

// kuzuVersionFromBuildInfo returns the linked go-kuzu version, or "" when build
// metadata does not carry it.
func kuzuVersionFromBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path == kuzuModulePath {
			return dep.Version
		}
	}
	return ""
}

// kuzuVersionFromGoMod returns the go-kuzu version required by the given go.mod
// content, or "" when it names no such requirement.
//
// This is a deliberately small parser rather than a golang.org/x/mod dependency:
// it reads one well-known line out of a file this module owns. It handles the
// two forms that line can take — inside a require block, or a single-line
// `require <path> <version>` — and ignores any trailing `// indirect` comment.
func kuzuVersionFromGoMod(goMod []byte) string {
	for _, line := range strings.Split(string(goMod), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "require" {
			fields = fields[1:] // single-line form: require <path> <version>
		}
		if len(fields) >= 2 && fields[0] == kuzuModulePath {
			return fields[1]
		}
	}
	return ""
}

// FormatStampPath returns the sidecar belonging to a database path.
func FormatStampPath(dbPath string) string {
	return dbPath + formatStampSuffix
}

// ReadFormatStamp returns the stamp beside a database, or (nil, nil) when the
// database predates stamping.
func ReadFormatStamp(dbPath string) (*FormatStamp, error) {
	data, err := os.ReadFile(FormatStampPath(dbPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read format stamp: %w", err)
	}
	var stamp FormatStamp
	if err := json.Unmarshal(data, &stamp); err != nil {
		// A corrupt stamp is treated as no stamp by CheckFormat; returning the
		// error here keeps the distinction available to callers that care.
		return nil, fmt.Errorf("parse format stamp %s: %w", FormatStampPath(dbPath), err)
	}
	return &stamp, nil
}

// WriteFormatStamp records this build's engine version beside a database.
//
// Written via a temporary file and a rename so an interrupted write leaves the
// previous stamp intact rather than a truncated one: a half-written stamp would
// read as corrupt and send a perfectly good database down the rebuild path.
func WriteFormatStamp(dbPath string, kgVersion string) error {
	stamp := FormatStamp{
		KuzuVersion: KuzuVersion(),
		KGVersion:   kgVersion,
		StampedAt:   time.Now().UTC(),
	}
	data, err := json.Marshal(stamp)
	if err != nil {
		return fmt.Errorf("encode format stamp: %w", err)
	}
	data = append(data, '\n')

	target := FormatStampPath(dbPath)
	tmp, err := os.CreateTemp(filepath.Dir(target), ".format-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp format stamp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write format stamp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync format stamp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close format stamp: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("install format stamp: %w", err)
	}
	return nil
}

// CheckFormat reports whether a database can be opened by this build, without
// opening it. See the note at the top of this file for why the check cannot be
// made from the open error instead.
func CheckFormat(dbPath string) (FormatStatus, *FormatStamp, error) {
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return FormatMissing, nil, nil
	} else if err != nil {
		return FormatMissing, nil, fmt.Errorf("stat database: %w", err)
	}

	stamp, err := ReadFormatStamp(dbPath)
	if err != nil {
		// Unreadable stamp: fall back to the unstamped path, which is
		// conservative — it defers to actually trying the open.
		return FormatUnstamped, nil, nil
	}
	if stamp == nil {
		return FormatUnstamped, nil, nil
	}
	if stamp.KuzuVersion != KuzuVersion() {
		return FormatMismatch, stamp, nil
	}
	return FormatOK, stamp, nil
}

// ArchiveDatabase moves a database aside so a fresh one can be built in its
// place, returning the path it was moved to. The journal and the stamp move
// with it: the journal is the input to the rebuild that follows, and leaving it
// beside the new database would replay it a second time on the next rebuild.
//
// Nothing is deleted. A database that could not be opened may still be the only
// copy of something, and a rename is cheap insurance against a rebuild that
// turns out worse than what it replaced.
func ArchiveDatabase(dbPath string) (string, error) {
	stamp, err := ReadFormatStamp(dbPath)
	label := "unknown"
	if err == nil && stamp != nil && stamp.KuzuVersion != "" {
		label = stamp.KuzuVersion
	}

	aside := fmt.Sprintf("%s.old-%s", dbPath, label)
	// A previous rebuild may have already claimed the name; never clobber it.
	for i := 2; ; i++ {
		if _, err := os.Stat(aside); errors.Is(err, os.ErrNotExist) {
			break
		}
		aside = fmt.Sprintf("%s.old-%s.%d", dbPath, label, i)
		if i > 100 {
			return "", fmt.Errorf("too many archived copies of %s", dbPath)
		}
	}

	if err := os.Rename(dbPath, aside); err != nil {
		return "", fmt.Errorf("archive database: %w", err)
	}
	// The WAL belongs to the archived database, not to its replacement.
	if err := os.Rename(dbPath+".wal", aside+".wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("archive write-ahead log: %w", err)
	}
	if err := os.Rename(FormatStampPath(dbPath), FormatStampPath(aside)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("archive format stamp: %w", err)
	}
	if err := os.Rename(JournalPath(dbPath), JournalPath(aside)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("archive journal: %w", err)
	}
	return aside, nil
}
