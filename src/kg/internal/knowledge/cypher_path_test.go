package knowledge

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCypherPathLiteralNormalisesSeparators(t *testing.T) {
	// The bug this guards against: a native Windows path carries backslashes
	// that Kuzu reads as escapes, so the COPY statement does not parse and the
	// indexer falls back to row-by-row inserts.
	got := cypherPathLiteral(`C:\Users\me\AppData\Local\Temp\kg-entities-1.json`)
	want := "C:/Users/me/AppData/Local/Temp/kg-entities-1.json"
	if runtime.GOOS == "windows" {
		if got != want {
			t.Errorf("cypherPathLiteral() = %q, want %q", got, want)
		}
		return
	}
	// On Unix a backslash is an ordinary filename character, so it must survive
	// as an escaped backslash rather than being rewritten into a separator.
	if !strings.Contains(got, `\\`) {
		t.Errorf("cypherPathLiteral() = %q, want backslashes escaped", got)
	}
}

func TestCypherPathLiteralEscapesQuote(t *testing.T) {
	// A single quote would otherwise close the literal early and leave the rest
	// of the path to be parsed as Cypher.
	got := cypherPathLiteral("/tmp/it's/kg.json")
	if !strings.Contains(got, `\'`) {
		t.Errorf("cypherPathLiteral() = %q, want the quote escaped", got)
	}
	if strings.Contains(strings.ReplaceAll(got, `\'`, ""), "'") {
		t.Errorf("cypherPathLiteral() = %q, left an unescaped quote", got)
	}
}

func TestCypherPathLiteralLeavesPlainPathAlone(t *testing.T) {
	plain := filepath.ToSlash(filepath.Join("tmp", "kg-entities.json"))
	if got := cypherPathLiteral(plain); got != plain {
		t.Errorf("cypherPathLiteral(%q) = %q, want it unchanged", plain, got)
	}
}
