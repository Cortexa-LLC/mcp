package main

import (
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cortexa-llc/mcp/kg/internal/knowledge"
	"github.com/spf13/cobra"
)

const graphTestProject = "graphcmd"

// seedGraphFixture creates a store holding app.go, which contains foo, and bar,
// which calls it. Closed for the caller when the test ends.
func seedGraphFixture(t *testing.T) *knowledge.Store {
	t.Helper()

	store, err := knowledge.OpenStore(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	file, err := store.CreateEntity("app.go", knowledge.EntityTypeFile, graphTestProject)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	fn, err := store.CreateEntity("foo", knowledge.EntityTypeFunction, graphTestProject)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	caller, err := store.CreateEntity("bar", knowledge.EntityTypeFunction, graphTestProject)
	if err != nil {
		t.Fatalf("CreateEntity: %v", err)
	}
	if err := store.CreateRelation(file.ID, fn.ID, knowledge.RelContains, graphTestProject); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	if err := store.CreateRelation(caller.ID, fn.ID, knowledge.RelCalls, graphTestProject); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	return store
}

func TestRunGraphRendersToWriter(t *testing.T) {
	store := seedGraphFixture(t)

	var out bytes.Buffer
	sub, err := runGraph(store, graphTestProject, graphSettings{
		Root:   "foo",
		Depth:  1,
		Format: "mermaid",
		Limit:  200,
	}, &out)
	if err != nil {
		t.Fatalf("runGraph: %v", err)
	}

	if len(sub.Nodes) != 3 {
		t.Errorf("got %d nodes, want 3 (foo plus both neighbours)", len(sub.Nodes))
	}
	if sub.Truncated {
		t.Error("Truncated = true, want false")
	}
	rendered := out.String()
	if !strings.HasPrefix(rendered, "%% kg graph:") {
		t.Errorf("output does not start with the provenance comment:\n%s", rendered)
	}
	for _, want := range []string{"graph LR", `"foo"`, `"app.go"`, "CONTAINS", "CALLS"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("output missing %q:\n%s", want, rendered)
		}
	}
}

func TestRunGraphFormats(t *testing.T) {
	store := seedGraphFixture(t)

	// The comment marker each format opens with is enough to tell them apart.
	for _, tc := range []struct{ format, wantPrefix string }{
		{format: "", wantPrefix: "%%"}, // an empty format means mermaid
		{format: "dot", wantPrefix: "//"},
		{format: "json", wantPrefix: "{"},
		{format: "DOT", wantPrefix: "//"},   // case-insensitive
		{format: " json ", wantPrefix: "{"}, // and space-tolerant
	} {
		var out bytes.Buffer
		if _, err := runGraph(store, graphTestProject, graphSettings{Format: tc.format, Limit: 200}, &out); err != nil {
			t.Fatalf("runGraph(%q): %v", tc.format, err)
		}
		if !strings.HasPrefix(out.String(), tc.wantPrefix) {
			t.Errorf("format %q produced output starting %q, want prefix %q",
				tc.format, firstLine(out.String()), tc.wantPrefix)
		}
	}
}

// Bad input must be rejected before the graph is loaded or anything is
// written, so a failed invocation never leaves a half-written document behind.
func TestRunGraphRejectsBadSettings(t *testing.T) {
	store := seedGraphFixture(t)

	for _, tc := range []struct {
		name     string
		settings graphSettings
		wantErr  string
	}{
		{
			name:     "unknown format",
			settings: graphSettings{Format: "svg"},
			wantErr:  "unknown format",
		},
		{
			name:     "unknown direction",
			settings: graphSettings{Direction: "sideways"},
			wantErr:  "unknown direction",
		},
		{
			name:     "negative depth",
			settings: graphSettings{Depth: -1},
			wantErr:  "--depth must not be negative",
		},
		{
			name:     "unknown root",
			settings: graphSettings{Root: "nosuchentity"},
			wantErr:  "no entity with ID or name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			_, err := runGraph(store, graphTestProject, tc.settings, &out)
			if err == nil {
				t.Fatalf("expected an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
			if out.Len() != 0 {
				t.Errorf("wrote %d bytes despite failing:\n%s", out.Len(), out.String())
			}
		})
	}
}

func TestRunGraphReportsTruncation(t *testing.T) {
	store := seedGraphFixture(t)

	var out bytes.Buffer
	sub, err := runGraph(store, graphTestProject, graphSettings{Limit: 1}, &out)
	if err != nil {
		t.Fatalf("runGraph: %v", err)
	}
	if !sub.Truncated {
		t.Error("Truncated = false, want true — the fixture has more nodes than the limit")
	}
	if !strings.Contains(out.String(), "TRUNCATED") {
		t.Errorf("rendered output does not admit it is incomplete:\n%s", out.String())
	}
}

// The flags carry the defaults a bare `kg graph` runs with; a change to any of
// them changes what every user gets without asking.
func TestGraphCommandDefaults(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{flag: "depth", want: "2"},
		{flag: "direction", want: "both"},
		{flag: "format", want: "mermaid"},
		{flag: "limit", want: "200"},
		{flag: "root", want: ""},
	} {
		f := graphCmd.Flags().Lookup(tc.flag)
		if f == nil {
			t.Errorf("--%s is not registered", tc.flag)
			continue
		}
		if f.DefValue != tc.want {
			t.Errorf("--%s default = %q, want %q", tc.flag, f.DefValue, tc.want)
		}
	}

	if !commandRegistered(t, "graph") {
		t.Error("graph command is not registered on the root command")
	}
}

// --personal names a single user-global database; there is nothing to
// federate, so asking for both is a mistake worth naming rather than quietly
// ignoring one of them.
func TestRunFederatedGraphRejectsPersonal(t *testing.T) {
	usePersonal = true
	t.Cleanup(func() { usePersonal = false })

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&out)
	_, err := runFederatedGraph(cmd, graphSettings{Limit: 10}, &out)
	if err == nil {
		t.Fatal("expected an error for --personal with --federated")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want it to say the flags are mutually exclusive", err)
	}
}

// Federation flags must fail before any database is opened, like the rest.
func TestRunFederatedGraphValidatesSettingsFirst(t *testing.T) {
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&out)
	if _, err := runFederatedGraph(cmd, graphSettings{Format: "svg"}, &out); err == nil {
		t.Error("expected an error for an unknown format")
	}
}

func TestGraphCommandFederationFlags(t *testing.T) {
	for _, tc := range []struct{ flag, want string }{
		{flag: "federated", want: "false"},
		{flag: "no-derived", want: "false"},
		{flag: "join-types", want: "[" + strings.Join(knowledge.DefaultJoinTypes, ",") + "]"},
		{flag: "join-max-layers", want: strconv.Itoa(knowledge.DefaultMaxJoinLayers)},
		{flag: "layer", want: "[]"},
	} {
		f := graphCmd.Flags().Lookup(tc.flag)
		if f == nil {
			t.Errorf("--%s is not registered", tc.flag)
			continue
		}
		if f.DefValue != tc.want {
			t.Errorf("--%s default = %q, want %q", tc.flag, f.DefValue, tc.want)
		}
	}
}

// "none" has to survive as an empty-but-not-nil policy: nil means "use the
// default types", and conflating the two would silently join everything the
// user asked to join nothing.
func TestJoinTypePolicy(t *testing.T) {
	if got := joinTypePolicy(false, knowledge.DefaultJoinTypes); got != nil {
		t.Errorf("unset flag = %v, want nil so the default policy applies", got)
	}

	got := joinTypePolicy(true, []string{"none"})
	if got == nil {
		t.Fatal("\"none\" produced nil, which means the default policy — it must join nothing")
	}
	if len(got) != 0 {
		t.Errorf("\"none\" = %v, want an empty policy", got)
	}

	if got := joinTypePolicy(true, []string{" NONE "}); got == nil || len(got) != 0 {
		t.Errorf("case and spacing changed the meaning of \"none\": %v", got)
	}

	if got := joinTypePolicy(true, []string{"package", "type"}); strings.Join(got, ",") != "package,type" {
		t.Errorf("explicit types = %v, want [package type]", got)
	}
}

func commandRegistered(t *testing.T, name string) bool {
	t.Helper()
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
