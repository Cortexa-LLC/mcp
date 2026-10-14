package kglib

// Project-namespaced primary keys.
//
// Entity and Observation node tables carry a single PRIMARY KEY column, `pk`,
// that namespaces the human-facing `id` by the project it belongs to. The
// `id` column stays exactly what it always was — "file:…", "function:…:F",
// or a UUID — so search, CLI display, and the journal's stable-id hint are
// unchanged; `pk` exists only to make the primary key unique across projects
// that share one database file.
//
// Why this is necessary: Kuzu v0.11.3 node tables support exactly one PRIMARY
// KEY column, so a composite (project_id, id) key is not expressible. Before
// `pk`, the key was the global `id` alone, but every clear/delete and nearly
// every read is scoped by project_id — so a surviving row from a different
// project_id with the same `id` would collide with the current run's entity and
// be silently dropped. Namespacing the key fixes that at the source.
//
// Separator choice: the ADR specified NUL, but go-kuzu marshals strings across
// cgo as C strings and truncates them at the first NUL byte — a NUL-joined pk
// would collapse to just the project_id in storage, re-introducing the very
// collisions this fixes (verified: every pk reduced to its project prefix). The
// Unit Separator (0x1F) is used instead: it is a non-printable control byte that
// survives the round trip and that neither a project_id (a repo directory base
// name) nor a code-derived id ("<kind>:<path>:<name>") nor a UUID ever contains.
const pkSeparator = "\x1f"

// PK derives the project-namespaced primary key for an entity or observation
// from its project_id and its local id.
//
// The Unit Separator byte is collision-free and unambiguously reversible given
// the invariant above (neither operand contains it), which is what lets a single
// `pk` column stand in for the composite (project_id, id) key Kuzu cannot
// express. Every write derives `pk` here, never ad hoc, so the format lives in
// exactly one place.
func PK(projectID, id string) string {
	return projectID + pkSeparator + id
}
