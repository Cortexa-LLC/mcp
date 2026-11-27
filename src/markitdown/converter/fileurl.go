package converter

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// fileURLPath converts a file:// URL into a native filesystem path.
//
// u.Path is a URL path, and on Windows that is not a filesystem path. The URL
// file:///C:/dir/page.html parses to the path "/C:/dir/page.html", and opening
// it verbatim fails with "The filename, directory name, or volume label syntax
// is incorrect" — the leading slash has to come off before the drive letter.
// A UNC location arrives as a host plus a path (file://server/share/page.html)
// and has to be rebuilt as \\server\share\page.html.
//
// On Unix the URL path is already the filesystem path and is returned as-is.
func fileURLPath(u *url.URL) string {
	// An empty host, or the conventional "localhost", both mean this machine.
	if host := u.Host; host != "" && !strings.EqualFold(host, "localhost") {
		return filepath.FromSlash("//" + host + u.Path)
	}

	p := u.Path
	// Trim the slash only ahead of a drive letter. A URL path such as
	// "/shared/page.html" has no drive, and trimming it would turn an absolute
	// path into a relative one.
	if runtime.GOOS == "windows" && hasDriveLetter(p) {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// hasDriveLetter reports whether a URL path begins with /X:, the shape a
// Windows drive takes once it has been through url.Parse.
func hasDriveLetter(p string) bool {
	if len(p) < 3 || p[0] != '/' || p[2] != ':' {
		return false
	}
	c := p[1]
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}
