package upk

// Entity types for user personal knowledge
const (
	EntityTypeConversation  = "conversation"
	EntityTypePerson        = "person"
	EntityTypeLearning      = "learning"
	EntityTypeInsight       = "insight"
	EntityTypeTopic         = "topic"
	EntityTypeProjectContext = "project_context"
)

// Relation types for user knowledge
const (
	RelDiscussedIn    = "DISCUSSED_IN"     // topic -> conversation
	RelLearnedFrom    = "LEARNED_FROM"     // insight -> learning
	RelAppliesTo      = "APPLIES_TO"       // learning -> project_context
	RelMentionedIn    = "MENTIONED_IN"     // topic -> conversation
	RelParticipatedIn = "PARTICIPATED_IN"  // person -> conversation
	RelRelatesTo      = "RELATES_TO"       // generic relation
	RelTaggedWith     = "TAGGED_WITH"      // entity -> topic
)

// AllRelTypes is the list of all relation types used by upk.
// This is used to initialize the kglib schema.
var AllRelTypes = []string{
	RelDiscussedIn,
	RelLearnedFrom,
	RelAppliesTo,
	RelMentionedIn,
	RelParticipatedIn,
	RelRelatesTo,
	RelTaggedWith,
}
