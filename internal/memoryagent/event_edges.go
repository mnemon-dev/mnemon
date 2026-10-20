package memoryagent

// eventEdgeKind is a kind of event edge: its name (the records' link type),
// its linking pair kind, the key prefix of its questions, its question about
// a new event n and a candidate c, and the question's yes and no criteria.
type eventEdgeKind struct {
	name     string
	pair     int
	key      string
	question func(n, c string) string
	yes, no  string
}

// eventEdgeKinds are the kinds of event edge, in the order a step asks them.
var eventEdgeKinds = []eventEdgeKind{
	{name: "semantic", pair: linkSemantic, key: "es", question: semanticQuestion, yes: semanticYes, no: semanticNo},
	{name: "temporal", pair: linkTemporal, key: "et", question: temporalQuestion, yes: temporalYes, no: temporalNo},
	{name: "causal", pair: linkCausal, key: "ec", question: causalQuestion, yes: causalYes, no: causalNo},
	{name: "entity", pair: linkEntity, key: "en", question: entityQuestion, yes: entityYes, no: entityNo},
}

// The event edge questions, in the style of the linking questions.

func semanticQuestion(n, c string) string {
	return "Are the new event whose `id` is \"" + n + "\" and the candidate whose `id` is \"" + c + "\" about the same matter, so that one helps to answer a question about the other?"
}

const (
	semanticYes = "They concern the same specific matter (the same plan, problem, decision, activity, object or relationship), even in different words."
	semanticNo  = "They only share a general topic, a person or a time, or concern different matters."
)

func temporalQuestion(n, c string) string {
	return "Is the new event whose `id` is \"" + n + "\" a later development of the same matter as the candidate whose `id` is \"" + c + "\"?"
}

const (
	temporalYes = "It continues, follows up, changes, completes or reports on the same matter after the candidate."
	temporalNo  = "It concerns a different matter, or only happens to be later."
)

func causalQuestion(n, c string) string {
	return "Did the candidate event whose `id` is \"" + c + "\" cause, enable or prevent the new event whose `id` is \"" + n + "\"?"
}

const (
	causalYes = "The records state or clearly imply that the candidate led to, made possible or stopped the new event."
	causalNo  = "No such link is stated or clearly implied; being earlier or related is not a cause."
)

func entityQuestion(n, c string) string {
	return "Are the new event whose `id` is \"" + n + "\" and the candidate whose `id` is \"" + c + "\" about the same particular person, organization, place, product or document?"
}

const (
	entityYes = "Both concern that same particular one, named or clearly referred to."
	entityNo  = "They only share a common name or a general kind of thing, or concern different ones."
)
