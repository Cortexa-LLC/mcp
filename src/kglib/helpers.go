package kglib

// stringOrEmpty performs a safe type assertion from an interface{} value to
// string. If the value is nil or is not a string the empty string is returned
// instead of panicking, which is the behaviour of a bare .(string) assertion.
func stringOrEmpty(v interface{}) string {
	s, _ := v.(string)
	return s
}
