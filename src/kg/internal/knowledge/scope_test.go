package knowledge

import "testing"

// TestMatchGlob covers the three glob shapes matchGlob supports. The
// "prefix/**/*" case regressed once: the suffix check sliced 4 bytes and
// compared against the 5-byte "/**/*", so the branch never fired and callers
// silently fell back to filepath.Match — which does not cross separators and
// therefore only matched paths with exactly as many segments as the pattern.
func TestMatchGlob(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"match-all", "**/*", "any/depth/at/all.scala", true},

		{"prefix direct child", "address/**/*", "address/README.md", true},
		{"prefix nested", "address/**/*", "address/app/clients/LoqateClient.scala", true},
		{"prefix deeply nested", "address/**/*", "address/a/b/c/d/e/f.scala", true},
		{"prefix bare dir", "address/**/*", "address", true},
		{"prefix other repo", "address/**/*", "price/app/Main.scala", false},
		{"prefix is not a partial segment", "address/**/*", "address-book/app/Main.scala", false},
		{"multi-segment prefix", "modules/CoreUI/**/*", "modules/CoreUI/src/View.swift", true},
		{"multi-segment prefix miss", "modules/CoreUI/**/*", "modules/Other/src/View.swift", false},

		{"suffix basename", "**/*.scala", "address/app/Main.scala", true},
		{"suffix basename miss", "**/*.scala", "address/app/Main.java", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchGlob(tt.pattern, tt.path); got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

// TestShouldIncludePath_RepoScopes exercises the per-repo scope shape used to
// federate a directory of sibling checkouts, where each scope owns one subtree.
func TestShouldIncludePath_RepoScopes(t *testing.T) {
	sc := &ScopeConfig{
		Name:     "address",
		Database: "address.db",
		Include:  []string{"address/**/*"},
	}

	included := []string{
		"address/app/clients/LoqateClient.scala",
		"address/project/Settings.scala",
		"address/README.md",
	}
	for _, p := range included {
		if !sc.ShouldIncludePath(p) {
			t.Errorf("ShouldIncludePath(%q) = false, want true", p)
		}
	}

	excluded := []string{
		"price/app/Main.scala",
		"depop-backend/manage.py",
		"README.md",
	}
	for _, p := range excluded {
		if sc.ShouldIncludePath(p) {
			t.Errorf("ShouldIncludePath(%q) = true, want false", p)
		}
	}
}

// TestShouldIncludePath_ExcludeWins verifies exclude patterns short-circuit
// includes — the documented "platform scope" shape from docs/kg-scopes.md.
func TestShouldIncludePath_ExcludeWins(t *testing.T) {
	sc := &ScopeConfig{
		Name:     "platform",
		Database: "platform.db",
		Include:  []string{"**/*"},
		Exclude:  []string{"modules/**/*"},
	}

	if sc.ShouldIncludePath("modules/FeatureA/src/A.swift") {
		t.Error("expected modules/** to be excluded")
	}
	if !sc.ShouldIncludePath("Sources/Networking/Client.swift") {
		t.Error("expected non-modules path to be included")
	}
}

// TestShouldIncludePath_IncludeModules covers the modules/ special case: only
// listed modules are indexed, while paths outside modules/ follow include rules.
func TestShouldIncludePath_IncludeModules(t *testing.T) {
	sc := &ScopeConfig{
		Name:           "team-a",
		Database:       "team-a.db",
		Include:        []string{"**/*"},
		IncludeModules: []string{"FeatureA"},
	}

	if !sc.ShouldIncludePath("modules/FeatureA/src/A.swift") {
		t.Error("expected listed module to be included")
	}
	if sc.ShouldIncludePath("modules/FeatureB/src/B.swift") {
		t.Error("expected unlisted module to be excluded")
	}
	if !sc.ShouldIncludePath("Sources/Shared/Util.swift") {
		t.Error("expected path outside modules/ to follow include rules")
	}
}
