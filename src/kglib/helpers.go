package kglib

import "time"

// stringOrEmpty performs a safe type assertion from an interface{} value to
// string. If the value is nil or is not a string the empty string is returned
// instead of panicking, which is the behaviour of a bare .(string) assertion.
func stringOrEmpty(v interface{}) string {
	s, _ := v.(string)
	return s
}

// timeOrZero converts a Kuzu TIMESTAMP column to time.Time.
//
// The go-kuzu driver returns TIMESTAMP values as time.Time. Older code here
// assumed int64 microseconds, which silently produced zero timestamps for every
// entity, observation, and relation — and, because calculateRecencyScore treats
// a zero time as "no boost", quietly disabled the recency component of hybrid
// search. int64 is still accepted so a driver change in either direction cannot
// reintroduce that failure mode.
func timeOrZero(v any) time.Time {
	switch ts := v.(type) {
	case time.Time:
		return ts.UTC()
	case int64:
		return time.UnixMicro(ts).UTC()
	default:
		return time.Time{}
	}
}
