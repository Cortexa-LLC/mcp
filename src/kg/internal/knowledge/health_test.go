package knowledge

import (
	"testing"
	"time"
)

// fakeAgeRow stands in for a query row so the NULL-aggregate path can be
// exercised without racing a real writer.
type fakeAgeRow struct {
	vals []any
	err  error
}

func (f fakeAgeRow) GetValue(col uint64) (any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.vals[col], nil
}

// A bare aggregate over zero matched rows returns one row of NULLs, not an
// empty result set — so NULL is what the max/min race actually looks like, and
// it must degrade to "no age" rather than failing the run. kg health is
// documented to always exit 0.
func TestAgeBoundsFromRow(t *testing.T) {
	newest := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	oldest := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("both present", func(t *testing.T) {
		age := &ObservationAge{}
		present, err := ageBoundsFromRow(fakeAgeRow{vals: []any{newest, oldest}}, age)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !present {
			t.Fatal("present = false, want true")
		}
		if !age.Newest.Equal(newest) || !age.Oldest.Equal(oldest) {
			t.Errorf("got newest=%v oldest=%v, want %v / %v", age.Newest, age.Oldest, newest, oldest)
		}
	})

	t.Run("NULL aggregate degrades to absent, not an error", func(t *testing.T) {
		age := &ObservationAge{}
		present, err := ageBoundsFromRow(fakeAgeRow{vals: []any{nil, nil}}, age)
		if err != nil {
			t.Fatalf("NULL bounds returned an error, which would fail the whole health run: %v", err)
		}
		if present {
			t.Error("present = true for NULL bounds")
		}
		if !age.Newest.IsZero() || !age.Oldest.IsZero() {
			t.Errorf("age was populated from NULLs: newest=%v oldest=%v", age.Newest, age.Oldest)
		}
	})

	t.Run("wrong type stays a hard error", func(t *testing.T) {
		age := &ObservationAge{}
		if _, err := ageBoundsFromRow(fakeAgeRow{vals: []any{"not-a-time", oldest}}, age); err == nil {
			t.Error("expected an error for a non-time value — silent zeroing is the bug this metric exists to catch")
		}
	})
}
