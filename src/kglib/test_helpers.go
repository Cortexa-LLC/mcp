package kglib

// testRelTypes provides all relation types used in tests
var testRelTypes = []string{
	"CONTAINS",
	"IMPORTS",
	"CALLS",
	"RELATES_TO",
	"BELONGS_TO",
	"FIXES",
	"SUPERSEDES",
	"CAUSED_BY",
	"DEPENDS_ON",
	"IMPLEMENTS",
	"TESTS",
	"DOCUMENTS",
}

// testSchemaConfig returns a SchemaConfig suitable for testing
func testSchemaConfig() *SchemaConfig {
	return &SchemaConfig{
		AdditionalRelTypes: testRelTypes,
	}
}
