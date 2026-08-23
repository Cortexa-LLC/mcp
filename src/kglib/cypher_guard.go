package kglib

import (
	"fmt"
	"regexp"
	"strings"
)

// writeMutatingKeywords lists Cypher/Kuzu keywords that mutate the database or
// touch the filesystem. Any query containing one of these words (case-insensitive,
// as a whole word, anywhere -- including inside a string literal) is rejected.
//
// The second group is not about graph mutation, it is about the host filesystem
// and process. Kuzu's read-only mode guards the *database*, not the machine, and
// this was confirmed empirically rather than assumed: against a handle opened via
// OpenStoreReadOnly,
//
//	COPY (MATCH (e:Entity) RETURN e.name) TO '/tmp/anything.csv'
//
// returned no error and wrote the file. EXPORT DATABASE '<dir>' likewise created a
// directory tree, and INSTALL <ext> reached out over the network to download and
// load a native extension. See TestReadOnlyModeDoesNotBlockFileWrites.
var writeMutatingKeywords = []string{
	// Graph mutation.
	"CREATE",
	"MERGE",
	"DELETE",
	"DETACH",
	"SET",
	"REMOVE",
	"DROP",
	"CALL",
	"FOREACH",
	// Filesystem, catalog and process reach.
	"LOAD",    // LOAD FROM '<path>' reads an arbitrary file
	"COPY",    // COPY (...) TO '<path>' writes an arbitrary file
	"EXPORT",  // EXPORT DATABASE '<dir>' writes a directory tree
	"IMPORT",  // IMPORT DATABASE '<dir>' reads and replays arbitrary DDL
	"ATTACH",  // ATTACH '<path>' mounts another database
	"INSTALL", // INSTALL <ext> downloads and loads a native extension
	"USE",     // USE <db> switches to an attached database
}

// readOnlyStatementStarters is the allow-list of clauses a read-only statement may
// begin with. This is the half of the guard that does not depend on having
// enumerated every dangerous verb: a Kuzu verb nobody here has heard of still fails
// because the statement does not start with one of these.
//
// EXPLAIN and PROFILE are deliberately absent. PROFILE executes its argument, so
// allowing the prefix would reopen the whole surface behind one word.
var readOnlyStatementStarters = []string{
	"MATCH",
	"OPTIONAL", // OPTIONAL MATCH
	"RETURN",
	"WITH",
	"UNWIND",
}

// writeMutatingPattern is a pre-compiled regex that matches any write keyword
// appearing as a standalone word (case-insensitive).
var writeMutatingPattern *regexp.Regexp

// leadingWordPattern extracts the first bare word of a statement.
var leadingWordPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)

func init() {
	// Build a single alternation pattern like:  (?i)\b(CREATE|MERGE|...)\b
	escaped := make([]string, len(writeMutatingKeywords))
	for i, kw := range writeMutatingKeywords {
		escaped[i] = regexp.QuoteMeta(kw)
	}
	pattern := `(?i)\b(` + strings.Join(escaped, "|") + `)\b`
	writeMutatingPattern = regexp.MustCompile(pattern)
}

// IsReadOnlyCypher returns nil when the query is safe to run through the
// agent-facing query_graph tool, and an error otherwise.
//
// Two independent checks must both pass, because either one alone has a known
// hole:
//
//  1. Deny-list. No word from writeMutatingKeywords may appear anywhere in the raw
//     query text. Scanning the raw text (literals and comments included) means no
//     quoting or commenting trick can hide a listed verb, at the cost of rejecting
//     the harmless MATCH (e) WHERE e.name = 'CREATE'. A deny-list can only ever be
//     as complete as the engine's verb list is known, which is check 2's job.
//
//  2. Allow-list. The query is stripped of comments and string literals, split into
//     `;`-separated statements, and every statement must begin with a clause from
//     readOnlyStatementStarters. This is what catches a write verb nobody listed.
//     It matters because Kuzu accepts `//` and block comments and multi-statement
//     input, so `/* MATCH */ COPY (...) TO '<path>'` and
//     `MATCH (e) RETURN e; COPY (...) TO '<path>'` both parse and both execute.
//
// What this does NOT guarantee, stated plainly so nobody reads more safety into it
// than is here:
//
//   - It is a lexical guard, not a parser. It does not understand Cypher grammar,
//     and it cannot reason about what a permitted read statement will cost. An
//     allowed MATCH can still be an unbounded cartesian product; use timeouts and
//     result caps for that, not this function.
//   - It says nothing about which rows the caller may see. Visibility and scope
//     filtering are enforced elsewhere.
//   - A query rejected here is not necessarily dangerous; the guard fails closed by
//     design, and legitimate queries do get caught (a listed word used as a
//     property name or inside a string literal, for instance).
//
// Anything malformed enough that the scanner cannot resolve it -- an unterminated
// string literal or block comment -- is rejected rather than guessed at.
func IsReadOnlyCypher(query string) error {
	if match := writeMutatingPattern.FindString(query); match != "" {
		return fmt.Errorf("write keyword %q is not allowed in read-only query_graph queries", strings.ToUpper(match))
	}

	stripped, err := stripCypherLiteralsAndComments(query)
	if err != nil {
		return fmt.Errorf("read-only query_graph queries must be lexically well-formed: %w", err)
	}

	for _, stmt := range strings.Split(stripped, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		word := strings.ToUpper(leadingWordPattern.FindString(stmt))
		if word == "" {
			return fmt.Errorf("read-only query_graph statements must begin with one of %s; found %q",
				strings.Join(readOnlyStatementStarters, ", "), firstRunes(stmt, 24))
		}
		if !containsFold(readOnlyStatementStarters, word) {
			return fmt.Errorf("statement starting with %q is not allowed in read-only query_graph queries; read-only statements must begin with one of %s",
				word, strings.Join(readOnlyStatementStarters, ", "))
		}
	}

	return nil
}

// stripCypherLiteralsAndComments replaces every string literal, quoted identifier
// and comment with a single space, leaving the statement skeleton behind. It exists
// so the allow-list sees real clause keywords and real statement separators: a `;`
// inside a string literal must not split a statement, and a comment must not be
// able to stand in front of a verb and hide it from the leading-word check.
//
// Escaping follows what Kuzu actually accepts, which was checked against the engine
// rather than assumed: backslash escapes a quote inside a literal, and a doubled
// quote does not (Kuzu's parser rejects 'a”b' outright). An unterminated literal or
// block comment returns an error so the caller fails closed.
func stripCypherLiteralsAndComments(query string) (string, error) {
	var b strings.Builder
	b.Grow(len(query))

	runes := []rune(query)
	for i := 0; i < len(runes); {
		c := runes[i]

		// Comments.
		if c == '/' && i+1 < len(runes) {
			switch runes[i+1] {
			case '/':
				i += 2
				for i < len(runes) && runes[i] != '\n' {
					i++
				}
				b.WriteByte(' ')
				continue
			case '*':
				i += 2
				closed := false
				for i+1 < len(runes) {
					if runes[i] == '*' && runes[i+1] == '/' {
						i += 2
						closed = true
						break
					}
					i++
				}
				if !closed {
					return "", fmt.Errorf("unterminated block comment")
				}
				b.WriteByte(' ')
				continue
			}
		}

		// String literals and quoted identifiers.
		if c == '\'' || c == '"' || c == '`' {
			quote := c
			i++
			closed := false
			for i < len(runes) {
				if runes[i] == '\\' && i+1 < len(runes) {
					i += 2
					continue
				}
				if runes[i] == quote {
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return "", fmt.Errorf("unterminated %c-quoted literal", quote)
			}
			b.WriteByte(' ')
			continue
		}

		b.WriteRune(c)
		i++
	}

	return b.String(), nil
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
