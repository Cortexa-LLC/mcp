package kglib

import (
	"fmt"
	"regexp"
	"strings"
)

// writeMutatingKeywords lists Cypher keywords that perform mutations.
// Any query containing one of these words (case-insensitive, as a whole word)
// is rejected by isReadOnlyCypher.
var writeMutatingKeywords = []string{
	"CREATE",
	"MERGE",
	"DELETE",
	"DETACH",
	"SET",
	"REMOVE",
	"DROP",
	"CALL",
	"LOAD",
	"FOREACH",
}

// writeMutatingPattern is a pre-compiled regex that matches any write keyword
// appearing as a standalone word (case-insensitive).
var writeMutatingPattern *regexp.Regexp

func init() {
	// Build a single alternation pattern like:  (?i)\b(CREATE|MERGE|...)\b
	escaped := make([]string, len(writeMutatingKeywords))
	for i, kw := range writeMutatingKeywords {
		escaped[i] = regexp.QuoteMeta(kw)
	}
	pattern := `(?i)\b(` + strings.Join(escaped, "|") + `)\b`
	writeMutatingPattern = regexp.MustCompile(pattern)
}

// IsReadOnlyCypher returns nil when the query contains only read operations
// (MATCH / RETURN / WITH / WHERE / ORDER BY / SKIP / LIMIT / UNWIND / AS /
// OPTIONAL MATCH, etc.).  It returns an error if any write-mode keyword is
// detected.
//
// The check is intentionally conservative: a keyword found anywhere in the
// query string (including inside string literals) causes rejection.  This
// errs on the side of safety; legitimate read queries should not require
// write keywords.
func IsReadOnlyCypher(query string) error {
	match := writeMutatingPattern.FindString(query)
	if match != "" {
		return fmt.Errorf("write keyword %q is not allowed in read-only query_graph queries", strings.ToUpper(match))
	}
	return nil
}
