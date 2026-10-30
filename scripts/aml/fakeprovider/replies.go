package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The replies, as the engine's callers parse them (internal/memoryagent):
//
//   - the simple strategy's planner (simple_plan.go): up to three searches
//     and a NOTE: line; NEED:/ALL: lines; one rewritten search per need not
//     found yet;
//   - the layered extraction (layered.go, timelines.go applyReplyWith): a
//     JSON object of events and facts that cite the batch's records (r0, r1,
//     …), with their session dates and entity names;
//   - a completer answering binary questions (provider.go Decide): a JSON
//     object of probabilities;
//   - JEV (provider.go parseDecision): a noul probability for every question.
//
// Every choice is a function of the request body's SHA-256, so the same
// request always gets the same bytes and runs are comparable.

// reply is a well-formed reply, and what malform needs of it.
type reply struct {
	value     any // as sent
	model, id string
	sum       []byte
	usage     any
	// content is a DeepSeek reply's content; jsonContent says its caller
	// parses it as JSON.
	content     string
	jsonContent bool
	// keys are a JEV request's question keys, in order.
	keys []string
}

// completion is the DeepSeek reply r with content in place of its own.
func (r reply) completion(content, finish string) map[string]any {
	return map[string]any{
		"id":      "fake-" + r.id,
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   r.model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": finish}},
		"usage":   r.usage,
	}
}

// completionRequest is what memoryagent's completion sends.
type completionRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// completion answers a chat completion with the content its caller parses.
func completion(body []byte) (reply, error) {
	var req completionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return reply{}, requestError("request body must be a JSON object")
	}
	system, prompt := "", ""
	for _, message := range req.Messages {
		switch message.Role {
		case "system":
			system = message.Content
		case "user":
			prompt = message.Content
		}
	}
	if req.Model == "" || prompt == "" {
		return reply{}, requestError("model and a user message are required")
	}
	sum := sha256.Sum256(body)
	content, jsonContent := completionContent(system, prompt, sum[:])
	r := reply{model: req.Model, id: hex.EncodeToString(sum[:6]), sum: sum[:], content: content, jsonContent: jsonContent,
		usage: map[string]int{"prompt_tokens": tokens(len(body)), "completion_tokens": tokens(len(content)), "total_tokens": tokens(len(body)) + tokens(len(content))}}
	r.value = r.completion(content, "stop")
	return r, nil
}

// decision answers a JEV request {"model":…,"state":…,"questions":{key:…}}
// with a noul probability for every question.
func decision(body []byte) (reply, error) {
	var req struct {
		Model     string                     `json:"model"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return reply{}, requestError("request body must be a JSON object")
	}
	if req.Model == "" || len(req.Questions) == 0 {
		return reply{}, requestError("model and at least one question are required")
	}
	sum := sha256.Sum256(body)
	r := reply{model: req.Model, sum: sum[:], keys: sortedKeys(req.Questions),
		usage: map[string]int{"input_tokens": tokens(len(body)), "output_tokens": 2 * len(req.Questions)}}
	answers := map[string]any{}
	for _, key := range r.keys {
		answers[key] = map[string]any{"type": "noul", "noul": probability(sum[:], key)}
	}
	r.value = map[string]any{"model": req.Model, "answers": answers, "usage": r.usage}
	return r, nil
}

// tokens is a rough token count of n bytes: four bytes a token, one at least.
func tokens(n int) int { return max(1, n/4) }

// completionContent picks the reply by what the prompt asks for, in the
// order the engine's prompts can be told apart, and says whether the
// caller parses it as JSON.
func completionContent(system, prompt string, sum []byte) (string, bool) {
	switch {
	case strings.Contains(prompt, "New records:"):
		return extraction(prompt, sum), true
	case strings.HasPrefix(system, "Answer the binary questions"):
		return binaryAnswers(prompt, sum), true
	case strings.Contains(prompt, "NOT FOUND YET:"):
		return rewrites(prompt, sum), false
	case strings.Contains(prompt, `"NEED:"`) || strings.Contains(system, "NEED:"):
		return needs(prompt, sum), false
	}
	return searches(prompt, sum), false
}

// The markers that end the conversation part of a planner's prompt.
var promptTasks = []string{"\n\nWrite up to", "\n\nList the facts", "\n\nSEARCHES ALREADY RUN:", "\n\nWrite one new search"}

// conversation is the dialogue part of a planner's prompt: what follows
// "CONVERSATION:" up to the task, or the whole prompt without one.
func conversation(prompt string) string {
	_, text, found := strings.Cut(prompt, "CONVERSATION:\n")
	if !found {
		text = prompt
	}
	for _, task := range promptTasks {
		if i := strings.Index(text, task); i >= 0 {
			text = text[:i]
		}
	}
	return text
}

// lastUserText is the last "user: " message of a planner's dialogue.
func lastUserText(dialogue string) string {
	start := strings.LastIndex(dialogue, "\nuser: ")
	switch {
	case start >= 0:
		start += len("\nuser: ")
	case strings.HasPrefix(dialogue, "user: "):
		start = len("user: ")
	default:
		return dialogue
	}
	text := dialogue[start:]
	if end := strings.Index(text, "\nassistant: "); end >= 0 {
		text = text[:end]
	}
	return text
}

var stopwords = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`the and for are but not you your yours with this that these those was were what when where which who whom whose why how
		have has had does did doing done can could would should will shall may might must about from into onto over under again then than
		there their them they she her hers him his its our ours out off any all some each every few more most other such only own same very
		just also too here now ever never one two get got make made like want need use used user assistant conversation session current date
		time today yes no let did tell said say says ask asked please thanks thank okay sure really much many lot lots well`) {
		stopwords[word] = true
	}
}

// words splits text into lowercase words of letters and digits.
func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// contentWords are text's words of at least three letters or digits that
// are not stopwords, each once, in order.
func contentWords(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, word := range words(text) {
		if len([]rune(word)) >= 3 && !stopwords[word] && !seen[word] {
			seen[word] = true
			out = append(out, word)
		}
	}
	return out
}

// pick is a number from sum and salt, for choices that must not vary
// between runs.
func pick(sum []byte, salt string) uint64 {
	mixed := sha256.Sum256(append(append([]byte{}, sum...), salt...))
	return binary.BigEndian.Uint64(mixed[:8])
}

// phrase is up to n of words starting at a position sum picks, wrapping.
func phrase(words []string, n int, sum []byte, salt string) string {
	if len(words) == 0 {
		return ""
	}
	start := int(pick(sum, salt) % uint64(len(words)))
	out := []string{}
	for i := 0; i < min(n, len(words)); i++ {
		out = append(out, words[(start+i)%len(words)])
	}
	return strings.Join(out, " ")
}

// questionWords are the content words of the last user message of the
// prompt's dialogue, or of the whole dialogue, or a stand-in.
func questionWords(prompt string) []string {
	dialogue := conversation(prompt)
	found := contentWords(lastUserText(dialogue))
	if len(found) == 0 {
		found = contentWords(dialogue)
	}
	if len(found) == 0 {
		found = []string{"earlier", "plans"}
	}
	return found
}

// searches is the cue plan: three searches of a few words each from the
// question, then the NOTE: line of an invented record.
func searches(prompt string, sum []byte) string {
	found := questionWords(prompt)
	lines := []string{}
	for i := range 3 {
		if line := phrase(found, 2+i%2, sum, "search"+strconv.Itoa(i)); line != "" && !contains(lines, line) {
			lines = append(lines, line)
		}
	}
	lines = append(lines, "NOTE: The user said earlier that "+phrase(found, 5, sum, "note")+" came up in their plans.")
	return strings.Join(lines, "\n")
}

// countingWords mark a question that needs every instance of something.
var countingWords = regexp.MustCompile(`(?i)\b(how many|how often|list|every|all the|total|count)\b`)

// needs is the need plan: one need, ALL: when the question counts or lists,
// and sometimes a second.
func needs(prompt string, sum []byte) string {
	found := questionWords(prompt)
	label := "NEED"
	if countingWords.MatchString(lastUserText(conversation(prompt))) {
		label = "ALL"
	}
	lines := []string{label + ": what the user said about " + phrase(found, 4, sum, "need0")}
	if len(found) > 4 && pick(sum, "need1")%3 == 0 {
		lines = append(lines, "NEED: when the user mentioned "+phrase(found, 2, sum, "need1"))
	}
	return strings.Join(lines, "\n")
}

// rewordings make a rewritten search differ from the searches already run.
var rewordings = []string{"details", "plans", "history", "changes", "dates"}

// rewrites are the loop's new searches: one for each need not found yet.
func rewrites(prompt string, sum []byte) string {
	_, missing, _ := strings.Cut(prompt, "NOT FOUND YET:\n")
	missing, _, _ = strings.Cut(missing, "\n\nWrite one new search")
	lines := []string{}
	for i, need := range strings.Split(missing, "\n") {
		need = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(need), "-"))
		if need == "" {
			continue
		}
		found := contentWords(need)
		if len(found) == 0 {
			found = []string{"earlier"}
		}
		extra := rewordings[pick(sum, "reword"+strconv.Itoa(i))%uint64(len(rewordings))]
		lines = append(lines, phrase(found, 2, sum, "rewrite"+strconv.Itoa(i))+" "+extra)
	}
	if len(lines) == 0 {
		lines = append(lines, "earlier plans")
	}
	return strings.Join(lines, "\n")
}

// The record headers of a fold's batch ("[r3] 2023/05/08 (session s1)") and
// the date in one.
var (
	recordHeader = regexp.MustCompile(`(?m)^\[r(\d+)\] ([^\n]*)$`)
	headerDate   = regexp.MustCompile(`\d{4}/\d{2}/\d{2}`)
)

// extractedEvent and extractedFact are the items of a layered extraction's
// reply, in the form layeredSystem asks for.
type extractedEvent struct {
	Thread   string   `json:"thread"`
	Date     string   `json:"date"`
	Text     string   `json:"text"`
	Entities []string `json:"entities"`
	Records  []string `json:"records"`
}

type extractedFact struct {
	Key      string   `json:"key"`
	Value    string   `json:"value"`
	Date     string   `json:"date"`
	Entities []string `json:"entities"`
	Records  []string `json:"records"`
	Conflict bool     `json:"conflict"`
}

// extraction is the fold's JSON: for each record of the batch an event on a
// thread named after the record's most frequent word, citing the record,
// and for about a third of them a fact; no directives.
func extraction(prompt string, sum []byte) string {
	_, batch, _ := strings.Cut(prompt, "New records:\n\n")
	events, facts := []extractedEvent{}, []extractedFact{}
	headers := recordHeader.FindAllStringSubmatchIndex(batch, -1)
	for i, at := range headers {
		end := len(batch)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		id, header, text := "r"+batch[at[2]:at[3]], batch[at[4]:at[5]], spoken(batch[at[1]:end])
		said := firstWords(text, 24)
		if said == "" {
			continue
		}
		date, names, topic := headerDate.FindString(header), entities(text), topicOf(text)
		events = append(events, extractedEvent{Thread: "new: " + topic, Date: date, Text: said, Entities: names, Records: []string{id}})
		if pick(sum, "fact"+id)%3 == 0 {
			facts = append(facts, extractedFact{Key: topic + " · status", Value: firstWords(text, 8), Date: date, Entities: names, Records: []string{id}})
		}
	}
	return string(encode(map[string]any{"events": events, "facts": facts, "directives": []any{}}))
}

// spoken is a record's text without the speaker labels.
func spoken(text string) string {
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		for _, label := range []string{"user: ", "assistant: "} {
			line = strings.TrimPrefix(line, label)
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " ")
}

// firstWords is text's first n words.
func firstWords(text string, n int) string {
	fields := strings.Fields(text)
	return strings.Join(fields[:min(n, len(fields))], " ")
}

// entities are up to three capitalized words of text that do not start a
// sentence and are not stopwords: the names a record mentions.
func entities(text string) []string {
	out := []string{}
	fields := strings.Fields(text)
	for i, field := range fields {
		name := strings.TrimFunc(field, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if i == 0 || len(name) < 3 || stopwords[strings.ToLower(name)] || strings.ContainsAny(fields[i-1], ".!?") {
			continue
		}
		if first := []rune(name)[0]; unicode.IsUpper(first) && !contains(out, name) {
			out = append(out, name)
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

// topicOf is text's most frequent content word, the first on ties.
func topicOf(text string) string {
	counts := map[string]int{}
	order := []string{}
	for _, word := range words(text) {
		if len([]rune(word)) < 3 || stopwords[word] {
			continue
		}
		if counts[word] == 0 {
			order = append(order, word)
		}
		counts[word]++
	}
	if len(order) == 0 {
		return "other"
	}
	sort.SliceStable(order, func(a, b int) bool { return counts[order[a]] > counts[order[b]] })
	return order[0]
}

// binaryAnswers is a completer's answer to Decide's prompt
// {"state":…,"questions":{key:…}}: a probability for every key.
func binaryAnswers(prompt string, sum []byte) string {
	var req struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if json.Unmarshal([]byte(prompt), &req) != nil {
		return "{}"
	}
	answers := map[string]float64{}
	for key := range req.Questions {
		answers[key] = probability(sum, key)
	}
	return string(encode(answers))
}

// probability is the answer to question key, in [0, 0.999].
func probability(sum []byte, key string) float64 {
	return float64(pick(sum, "answer:"+key)%1000) / 1000
}
