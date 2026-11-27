package knowledge

import (
	"path/filepath"
	"strings"
)

// cypherPathLiteral renders a filesystem path for use inside a single-quoted
// Cypher string literal, as the COPY ... FROM '<path>' bulk loads need.
//
// Kuzu's parser treats a backslash inside a string literal as an escape
// character, so interpolating a native Windows path produces a statement it
// cannot parse at all:
//
//	COPY Entity(...) FROM 'C:\Users\me\AppData\Local\Temp\kg-entities-1.json'
//	=> Parser exception: extraneous input ''' expecting {...}
//
// Every COPY in the indexer failed that way on Windows and silently fell back
// to row-by-row inserts, which still produce a correct graph but give up the
// bulk loader's O(N) advantage — the whole reason these paths exist.
//
// Forward slashes are accepted by Kuzu on every platform it runs on, including
// Windows, so the path is normalised rather than escaped. The remaining
// replacements matter only on Unix, where a backslash or a single quote is a
// legal filename character and would otherwise let a path terminate the literal
// early.
func cypherPathLiteral(path string) string {
	p := filepath.ToSlash(path)
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `'`, `\'`)
	return p
}
