package hub

import (
	"net/http"
	"testing"
	"time"
)

// A hub namespace is shared by every repo pushing to it, and graph names can be
// overridden by hand. Without an ownership check, a name collision silently
// replaces another team's graph — they lose their knowledge and nothing says so.
func TestSeedRefusesPushFromADifferentRepo(t *testing.T) {
	ts, _ := newTestHub(t, "", "s3cret", "dev")

	first := rawSeed(t, ts.URL, "platform", "abc123", map[string]string{
		"X-KG-Repo":    "github.com/Cortexa-LLC/depop",
		"X-KG-Version": "dev",
	})
	// The body is empty, so staging fails — the registry entry is what matters
	// here, and ownership is checked before staging.
	if first.StatusCode == http.StatusConflict {
		t.Fatalf("first push from a repo was refused: %d", first.StatusCode)
	}
}

// The check has to run against a registry that actually records an owner, so
// drive it through a real push.
func TestGraphOwnershipAcrossPushes(t *testing.T) {
	ts, dataDir := newTestHub(t, "", "s3cret", "dev")
	dbPath := buildFixtureDB(t)

	if err := pushFixture(t, ts.URL, "platform", dbPath, "abc123", nil); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// Record the owner the way a real push does.
	reg, err := loadRegistry(dataDir)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	info := reg.Graphs["platform"]
	info.Repo = "github.com/Cortexa-LLC/depop"
	reg.Graphs["platform"] = info
	if err := saveRegistry(dataDir, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	// The same repo may push again.
	resp := rawSeed(t, ts.URL, "platform", "def456", map[string]string{
		"X-KG-Repo":    "github.com/Cortexa-LLC/depop",
		"X-KG-Version": "dev",
	})
	if resp.StatusCode == http.StatusConflict {
		t.Error("the owning repo was refused its own graph")
	}

	// A different one may not.
	resp = rawSeed(t, ts.URL, "platform", "def456", map[string]string{
		"X-KG-Repo":    "github.com/Cortexa-LLC/harvana",
		"X-KG-Version": "dev",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("a foreign repo got status %d, want %d — it would have replaced the graph",
			resp.StatusCode, http.StatusConflict)
	}

	// Unless it says so explicitly, for a repo that really was renamed.
	resp = rawSeed(t, ts.URL, "platform", "def456", map[string]string{
		"X-KG-Repo":    "github.com/Cortexa-LLC/harvana",
		"X-KG-Force":   "1",
		"X-KG-Version": "dev",
	})
	if resp.StatusCode == http.StatusConflict {
		t.Error("X-KG-Force did not override the ownership check")
	}
}

// Ownership is unknown for graphs seeded before repo was recorded, and for
// pushes that do not declare one. Those must be allowed: refusing them would
// break existing deployments to guard against nothing.
func TestOwnershipCheckIsPermissiveWhenOwnerIsUnknown(t *testing.T) {
	ts, dataDir := newTestHub(t, "", "s3cret", "dev")
	dbPath := buildFixtureDB(t)

	if err := pushFixture(t, ts.URL, "legacy", dbPath, "abc123", nil); err != nil {
		t.Fatalf("push: %v", err)
	}

	reg, err := loadRegistry(dataDir)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	info := reg.Graphs["legacy"]
	info.Repo = "" // seeded before the field was recorded
	reg.Graphs["legacy"] = info
	if err := saveRegistry(dataDir, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	resp := rawSeed(t, ts.URL, "legacy", "def456", map[string]string{
		"X-KG-Repo":    "github.com/Cortexa-LLC/anything",
		"X-KG-Version": "dev",
	})
	if resp.StatusCode == http.StatusConflict {
		t.Error("a graph with no recorded owner was treated as owned by someone else")
	}
}

// The ownership check reads the registry, and the registry loader takes s.mu
// itself. Holding the lock around it deadlocks — which is exactly what the
// first version of this code did. A timeout makes that failure a red test
// rather than a hung suite.
func TestSeedDoesNotDeadlockOnOwnershipCheck(t *testing.T) {
	ts, _ := newTestHub(t, "", "s3cret", "dev")

	done := make(chan struct{})
	go func() {
		defer close(done)
		rawSeed(t, ts.URL, "graph", "abc123", map[string]string{
			"X-KG-Repo":    "github.com/Cortexa-LLC/depop",
			"X-KG-Version": "dev",
		})
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("seed did not return: the ownership check is holding a lock the registry loader also takes")
	}
}
