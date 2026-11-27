package converter

import (
	"context"
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
	got, err := fileURLPath(u)
	if err != nil {
		t.Fatalf("fileURLPath: %v", err)
	}
	if got != native {
		t.Errorf("fileURLPath() = %q, want %q", got, native)
	}
}

func TestFileURLPathStripsDriveLetterSlash(t *testing.T) {
	u, err := url.Parse("file:///C:/dir/page.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := fileURLPath(u)
	if err != nil {
		t.Fatalf("fileURLPath: %v", err)
	}

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
	got, err := fileURLPath(u)
	if err != nil {
		t.Fatalf("fileURLPath: %v", err)
	}
	if want := filepath.FromSlash("/shared/page.html"); got != want {
		t.Errorf("fileURLPath() = %q, want %q", got, want)
	}
}

func TestFileURLPathRejectsRemoteHost(t *testing.T) {
	// Reading file://host/share/file as the UNC path \\host\share\file would
	// make Windows dial SMB on a caller-supplied host, offering up NTLM
	// credentials to relay and turning a conversion into an outbound request to
	// an arbitrary address. It has to be refused, not translated.
	for _, raw := range []string{
		"file://evil.example.com/share/page.html",
		"file://10.0.0.5/c$/page.html",
		"file://LOCALHOST.evil.example.com/share/page.html",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		got, err := fileURLPath(u)
		if err == nil {
			t.Errorf("fileURLPath(%q) = %q, want an error", raw, got)
			continue
		}
		// Nothing resembling a path may come back alongside the error: a caller
		// that ignores err must not be handed a UNC path to open.
		if got != "" {
			t.Errorf("fileURLPath(%q) returned path %q with its error; want empty", raw, got)
		}
		if !strings.Contains(err.Error(), "remote host") {
			t.Errorf("fileURLPath(%q) error = %v, want it to name the remote host", raw, err)
		}
	}
}

func TestFileURLPathTreatsLocalhostAsLocal(t *testing.T) {
	// file://localhost/... is the spec's way of saying "this machine", so it is
	// local rather than remote, and must not be rebuilt as the UNC host
	// \\localhost either.
	for _, raw := range []string{
		"file://localhost/shared/page.html",
		"file://LocalHost/shared/page.html", // the comparison is case-insensitive
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		got, err := fileURLPath(u)
		if err != nil {
			t.Fatalf("fileURLPath(%q): %v", raw, err)
		}
		if want := filepath.FromSlash("/shared/page.html"); got != want {
			t.Errorf("fileURLPath(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestConvertURIRejectsRemoteFileHost(t *testing.T) {
	// The rejection has to hold at the exported entry point, not just in the
	// helper: ConvertURI is what the MCP tool description advertises as
	// accepting file: URIs.
	_, err := newTestConverter().ConvertURI(
		context.Background(), "file://evil.example.com/share/page.html")
	if err == nil {
		t.Fatal("ConvertURI accepted a file URL on a remote host")
	}
	if !strings.Contains(err.Error(), "remote host") {
		t.Errorf("ConvertURI error = %v, want it to name the remote host", err)
	}
}
