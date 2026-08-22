package knowledge

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// StampMeta records index provenance into the store: the git commit and origin
// URL of root, whether the working tree was dirty, the configured embedding
// model, and the kg version. Non-git roots stamp with an empty commit rather
// than failing — provenance is best-effort, never a reason to fail an index.
//
// KNOWLEDGE_EMBED_MODEL is recorded verbatim (empty when unset): kg index does
// not itself generate embeddings today, so the stamp records the configured
// model, not a guarantee that embeddings exist.
func StampMeta(store *Store, projectID, root, kgVersion string) (*KGMeta, error) {
	meta := KGMeta{
		ProjectID:  projectID,
		IndexedAt:  time.Now().UTC(),
		EmbedModel: os.Getenv("KNOWLEDGE_EMBED_MODEL"),
		KGVersion:  kgVersion,
	}
	meta.Commit, meta.RepoURL, meta.Dirty = GitProvenance(root)
	if err := store.SetMeta(meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// GitProvenance inspects root's git state and returns the HEAD commit, the
// origin URL, and whether the working tree is dirty. Best-effort: a non-git
// root returns an empty commit rather than an error.
func GitProvenance(root string) (commit, repoURL string, dirty bool) {
	out, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", false
	}
	commit = out
	// remote.origin.url may legitimately be absent (no remote configured);
	// the ignored error is deliberate.
	repoURL, _ = gitOutput(root, "config", "--get", "remote.origin.url")
	if status, err := gitOutput(root, "status", "--porcelain"); err == nil && status != "" {
		dirty = true
	}
	return commit, repoURL, dirty
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// GitAheadCount returns how many commits dir's HEAD is ahead of commit.
// Returns an error when commit is unknown to the local history (e.g. after a
// rebase) or dir is not a git repository.
func GitAheadCount(dir, commit string) (int, error) {
	out, err := gitOutput(dir, "rev-list", "--count", commit+"..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}
