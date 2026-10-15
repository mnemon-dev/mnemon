package judge

import (
	"encoding/json"
	"fmt"
)

// wireQuestion is one entry of the request "questions" object. name is the
// map key and is not serialized into the question itself.
type wireQuestion struct {
	name         string
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type wireRequest struct {
	Model     string                  `json:"model"`
	State     json.RawMessage         `json:"state"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireResponse struct {
	Answers map[string]wireAnswer `json:"answers"`
}

// wireAnswer holds the union of noul and choice answer fields. Pointers
// distinguish a missing field from a zero value.
type wireAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    *float64           `json:"confidence"`
}

// inUnit reports whether v is a finite probability. NaN fails every
// comparison, so it is rejected along with out-of-range values.
func inUnit(v float64) bool { return v >= 0 && v <= 1 }

// expect rejects an answer whose declared type differs from the asked type.
func (a wireAnswer) expect(kind string) error {
	if a.Type != "" && a.Type != kind {
		return fmt.Errorf("answer type is %q, want %q", a.Type, kind)
	}
	return nil
}

// choice validates a choice answer against the options that were offered.
func (a wireAnswer) choice(options map[string]string) (ChoiceAnswer, error) {
	if err := a.expect("choice"); err != nil {
		return ChoiceAnswer{}, err
	}
	if a.Choice == nil {
		return ChoiceAnswer{}, fmt.Errorf("answer has no choice")
	}
	if _, ok := options[*a.Choice]; !ok {
		return ChoiceAnswer{}, fmt.Errorf("choice %q is not an offered option", *a.Choice)
	}
	for option, p := range a.Probabilities {
		if !inUnit(p) {
			return ChoiceAnswer{}, fmt.Errorf("probability for %q is out of range", option)
		}
	}
	p, ok := a.Probabilities[*a.Choice]
	if !ok {
		if a.Confidence == nil {
			return ChoiceAnswer{}, fmt.Errorf("answer has no probability for choice %q", *a.Choice)
		}
		p = *a.Confidence
	}
	if !inUnit(p) {
		return ChoiceAnswer{}, fmt.Errorf("probability for choice %q is out of range", *a.Choice)
	}
	return ChoiceAnswer{Choice: *a.Choice, Probability: p}, nil
}
