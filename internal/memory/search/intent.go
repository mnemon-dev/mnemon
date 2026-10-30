package search

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

// Intent represents the detected query intent.
type Intent string

const (
	IntentWhy     Intent = "WHY"
	IntentWhen    Intent = "WHEN"
	IntentEntity  Intent = "ENTITY"
	IntentGeneral Intent = "GENERAL"
)

// IntentWeights maps intent to edge type weights for traversal.
type IntentWeights map[model.EdgeType]float64

var intentWeightsMap = map[Intent]IntentWeights{
	IntentWhy: {
		model.EdgeCausal:   0.70,
		model.EdgeTemporal: 0.20,
		model.EdgeEntity:   0.05,
		model.EdgeSemantic: 0.05,
	},
	IntentWhen: {
		model.EdgeTemporal: 0.65,
		model.EdgeCausal:   0.15,
		model.EdgeEntity:   0.10,
		model.EdgeSemantic: 0.10,
	},
	IntentEntity: {
		model.EdgeEntity:   0.55,
		model.EdgeSemantic: 0.30,
		model.EdgeTemporal: 0.05,
		model.EdgeCausal:   0.10,
	},
	IntentGeneral: {
		model.EdgeTemporal: 0.25,
		model.EdgeSemantic: 0.25,
		model.EdgeCausal:   0.25,
		model.EdgeEntity:   0.25,
	},
}

var whyPatterns = regexp.MustCompile(
	`(?i)(why|reason|because|cause|motivation|rationale)|` +
		`(为什么|為什麼|為甚麼|原因|理由)`)

var whenPatterns = regexp.MustCompile(
	`(?i)(when|timeline|time|date|before|after|during|history|sequence)|` +
		`(什么时候|什麼時候|甚麼時候|何时|何時|时间|時間|之前|之后|之後)`)

var entityPatterns = regexp.MustCompile(
	`(?i)(what is|who is|tell me about|describe|about)|` +
		`(是什么|是什麼|是甚麼|谁是|誰是|关于|關於|介绍|介紹)`)

// IntentFromString parses a user-provided intent string into an Intent value.
func IntentFromString(s string) (Intent, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "WHY":
		return IntentWhy, nil
	case "WHEN":
		return IntentWhen, nil
	case "ENTITY":
		return IntentEntity, nil
	case "GENERAL":
		return IntentGeneral, nil
	default:
		return "", fmt.Errorf("unknown intent %q; valid: WHY, WHEN, ENTITY, GENERAL", s)
	}
}

// DetectIntent selects an intent using bounded, language-specific lexical cues.
// It preserves English/Chinese scoring; conflicting cues involving the additional
// languages fall back to GENERAL. This is not semantic language understanding.
func DetectIntent(query string) Intent {
	whyScore := legacyIntentScore(whyPatterns, query)
	whenScore := legacyIntentScore(whenPatterns, query)
	entityScore := legacyIntentScore(entityPatterns, query)

	questionIntent, conflict := multilingualQuestionIntent(query)
	if conflict {
		return IntentGeneral
	}
	if questionIntent != IntentGeneral {
		// A strong cue in another language must not override conflicting legacy
		// cues, even when one legacy intent has a higher keyword count.
		if (whyScore > 0 && questionIntent != IntentWhy) ||
			(whenScore > 0 && questionIntent != IntentWhen) ||
			(entityScore > 0 && questionIntent != IntentEntity) {
			return IntentGeneral
		}
		return questionIntent
	}

	if whyScore > whenScore && whyScore > entityScore && whyScore > 0 {
		return IntentWhy
	}
	if whenScore > whyScore && whenScore > entityScore && whenScore > 0 {
		return IntentWhen
	}
	if entityScore > 0 {
		return IntentEntity
	}
	return IntentGeneral
}

// GetWeights returns the edge type weights for the given intent.
func GetWeights(intent Intent) IntentWeights {
	w, ok := intentWeightsMap[intent]
	if !ok {
		return intentWeightsMap[IntentGeneral]
	}
	return w
}
