package memoryagent

import "slices"

// The forget check (safety.forget): what a user asks the assistant to forget
// is withdrawn from every read. The extraction reads the meaning: asked to
// (forgetFold), it writes such a request as a value of the key it concerns
// with "forget" set. The rest is engineering. Every value of the key up to
// its last forget mark is withdrawn, and the key shows only that it was
// forgotten. The records those values and the mark come from are withheld
// from the View, and so is an event or a directive that comes from them
// alone. A value told after the mark shows again.

// forgottenValue is what a forgotten key shows.
const forgottenValue = "forgotten at the user's request"

// forgetFold is spec with the extraction asked to mark a request to forget.
func forgetFold(spec foldSpec) foldSpec {
	spec.system = edited(spec.system,
		`Set "conflict" to true when a value contradicts an earlier one in this batch and the user does not say it changed.`,
		`Set "conflict" to true when a value contradicts an earlier one in this batch and the user does not say it changed. When the user asks to forget, delete or no longer keep something they told, give the key it concerns with "forget" set to true and, as the value, what is to be forgotten, briefly.`,
		`"records":["r15"],"conflict":false}`,
		`"records":["r15"],"conflict":false,"forget":false}`,
	)
	return spec
}

// forgets reports whether the agent runs the forget check.
func (a *Agent) forgets() bool { return a.config.Safety != nil && a.config.Safety.Forget }

// forgetting is m with what was forgotten withdrawn, and the ids of the
// records withheld; m as it is and nil when no key holds a forget mark. A
// key with a mark keeps the values after its last mark, or else one value,
// the mark's date and forgottenValue with no record. Every other value,
// event and directive loses the withheld records it cites, and an event or
// directive that cited only withheld records is dropped.
func forgetting(m timelineMemory) (timelineMemory, map[string]bool) {
	withheld, marks := map[string]bool{}, map[string]int{}
	for _, key := range m.FactOrder {
		values := m.Facts[key]
		last := -1
		for i, v := range values {
			if v.Forget {
				last = i
			}
		}
		if last < 0 {
			continue
		}
		marks[key] = last
		for _, v := range values[:last+1] {
			for _, id := range v.Records {
				withheld[id] = true
			}
		}
	}
	if len(marks) == 0 {
		return m, nil
	}
	kept := func(ids []string) []string {
		return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return withheld[id] })
	}
	out := timelineMemory{Threads: m.Threads, Facts: map[string][]factValue{}, FactOrder: m.FactOrder}
	for _, e := range m.Events {
		if len(e.Records) > 0 {
			if e.Records = kept(e.Records); len(e.Records) == 0 {
				continue
			}
		}
		out.Events = append(out.Events, e)
	}
	for _, d := range m.Directives {
		if len(d.Records) > 0 {
			if d.Records = kept(d.Records); len(d.Records) == 0 {
				continue
			}
		}
		out.Directives = append(out.Directives, d)
	}
	for _, key := range m.FactOrder {
		values := m.Facts[key]
		if last, marked := marks[key]; marked {
			if values = values[last+1:]; len(values) == 0 {
				out.Facts[key] = []factValue{{Value: forgottenValue, Date: m.Facts[key][last].Date, Records: []string{}, Forget: true}}
				continue
			}
		}
		out.Facts[key] = []factValue{}
		for _, v := range values {
			v.Records = kept(v.Records)
			out.Facts[key] = append(out.Facts[key], v)
		}
	}
	return out, withheld
}
