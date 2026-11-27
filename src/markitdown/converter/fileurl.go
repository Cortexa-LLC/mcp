package converter

import (
	"fmt"
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
//
// On Unix the URL path is already the filesystem path and is returned as-is.
func fileURLPath(u *url.URL) (string, error) {
	// A host names a file on some other machine, and this refuses to go and get
	// it. The natural Windows reading of file://host/share/file is the UNC path
	// \\host\share\file, and opening that makes the OS dial SMB on a host the
	// caller chose: it hands an attacker-supplied server the user's NTLM
	// credentials to relay, and turns "convert this document" into an outbound
	// network request to an arbitrary address. A document converter has no
	// legitimate reason to follow one.
	//
	// An empty host, and the conventional "localhost", both mean this machine.
	if host := u.Host; host != "" && !strings.EqualFold(host, "localhost") {
		return "", fmt.Errorf(
			"file URL names the remote host %q; only file URLs on this machine are supported", host)
	}

	p := u.Path
	// Trim the slash only ahead of a drive letter. A URL path such as
	// "/shared/page.html" has no drive, and trimming it would turn an absolute
	// path into a relative one.
	if runtime.GOOS == "windows" && hasDriveLetter(p) {
		p = p[1:]
	}
	return filepath.FromSlash(p), nil
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
