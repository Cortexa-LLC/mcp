package converter

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fileURI builds a valid file:// URL for a native path.
//
// Concatenating "file://" with the path directly is what the ConvertURI test
// used to do, and it only works on Unix: a Windows path produces
// "file://C:\dir\page.html", where url.Parse reads "C:" as the host and
// rejects the backslashes outright.
func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // the drive letter needs a root slash: /C:/dir/page.html
	}
	return (&url.URL{Scheme: schemeFile, Path: p}).String()
}

func TestFileURIRoundTripsThroughFileURLPath(t *testing.T) {
	// The property that matters: whatever fileURI produces for a native path,
	// fileURLPath must turn back into that same native path.
	native := filepath.Join(t.TempDir(), "page.html")

	u, err := url.Parse(fileURI(native))
	if err != nil {
		t.Fatalf("parse %q: %v", fileURI(native), err)
	}
	if got := fileURLPath(u); got != native {
		t.Errorf("fileURLPath() = %q, want %q", got, native)
	}
}

func TestFileURLPathStripsDriveLetterSlash(t *testing.T) {
	u, err := url.Parse("file:///C:/dir/page.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := fileURLPath(u)

	if runtime.GOOS == "windows" {
		if want := `C:\dir\page.html`; got != want {
			t.Errorf("fileURLPath() = %q, want %q", got, want)
		}
		return
	}
	// On Unix there is no drive letter to strip, so the URL path stands.
	if want := "/C:/dir/page.html"; got != want {
		t.Errorf("fileURLPath() = %q, want %q", got, want)
	}
}

func TestFileURLPathKeepsRootedPathWithoutDrive(t *testing.T) {
	// "/shared/page.html" has no drive; trimming the slash would make it
	// relative and resolve against the process's working directory.
	u, err := url.Parse("file:///shared/page.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := filepath.FromSlash("/shared/page.html")
	if got := fileURLPath(u); got != want {
		t.Errorf("fileURLPath() = %q, want %q", got, want)
	}
}

func TestFileURLPathHandlesUNCHost(t *testing.T) {
	u, err := url.Parse("file://server/share/page.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := filepath.FromSlash("//server/share/page.html")
	if got := fileURLPath(u); got != want {
		t.Errorf("fileURLPath() = %q, want %q", got, want)
	}
}

func TestFileURLPathTreatsLocalhostAsLocal(t *testing.T) {
	// file://localhost/... is the spec's way of saying "this machine", so it
	// must not be rebuilt as the UNC host \\localhost.
	u, err := url.Parse("file://localhost/shared/page.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := filepath.FromSlash("/shared/page.html")
	if got := fileURLPath(u); got != want {
		t.Errorf("fileURLPath() = %q, want %q", got, want)
	}
}
