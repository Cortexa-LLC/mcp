package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The JVM languages declare a file's package as a dotted namespace, and until
// now the indexer minted a `package` entity only for Go — whose package clause
// is a bare identifier. That gap made cross-layer package linking inert on the
// corpus it was designed for: LinkPackages requires at least
// minPackageSegments dotted segments, which no Go package name can ever reach.
//
// Each case indexes a real source file end to end rather than calling the
// extractor directly, so the assertion covers the grammar node type, the
// extractor, and the config wiring together.
func TestIndexerMintsPackageEntitiesPerLanguage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cases := []struct {
		name     string
		file     string
		source   string
		wantName string
		// wantSegments is how many dotted segments the name carries.
		// Cross-layer package linking requires a namespace specific enough to
		// be worth matching (three segments, at the time of writing), which is
		// exactly what the JVM languages give and Go does not. Asserted as a
		// plain count rather than importing that threshold, so this test does
		// not depend on the linking feature shipping.
		wantSegments int
	}{
		{
			name:         "java",
			file:         "Auth.java",
			source:       "package com.depop.auth.client;\n\npublic class Auth {}\n",
			wantName:     "com.depop.auth.client",
			wantSegments: 4,
		},
		{
			name:         "kotlin",
			file:         "Auth.kt",
			source:       "package com.depop.auth.client\n\nclass Auth\n",
			wantName:     "com.depop.auth.client",
			wantSegments: 4,
		},
		{
			name:         "scala",
			file:         "Auth.scala",
			source:       "package com.depop.auth.client\n\nclass Auth\n",
			wantName:     "com.depop.auth.client",
			wantSegments: 4,
		},
		{
			// Go keeps working exactly as before: a bare identifier, which is
			// correct for Go and simply too unspecific for cross-layer linking.
			name:         "go",
			file:         "auth.go",
			source:       "package auth\n\nfunc Hello() {}\n",
			wantName:     "auth",
			wantSegments: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			srcDir := filepath.Join(tmpDir, "src")
			if err := os.MkdirAll(srcDir, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(filepath.Join(srcDir, tc.file), []byte(tc.source), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			store, _ := runIndexer(t, srcDir, filepath.Join(tmpDir, "test.db"))

			if !entityExistsByName(t, store, tc.wantName, EntityTypePackage) {
				t.Fatalf("no %s entity named %q — the indexer minted no package for %s",
					EntityTypePackage, tc.wantName, tc.name)
			}

			// The point of the JVM cases: the name is a namespace, not a bare
			// word, so it is specific enough to be worth matching across layers.
			if got := len(strings.Split(tc.wantName, ".")); got != tc.wantSegments {
				t.Errorf("%q has %d dotted segment(s), want %d", tc.wantName, got, tc.wantSegments)
			}
		})
	}
}
