package manifest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type Trace struct {
	Step      string    `json:"step"`
	Type      string    `json:"type"`
	At        time.Time `json:"at"`
	Message   string    `json:"message,omitempty"`
	Condition *bool     `json:"condition,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

// Evaluate uses scalar types; the string "1" never equals the number 1.
func Evaluate(c *Condition, vars map[string]any) bool {
	if c == nil {
		return false
	}
	var got any = vars
	for _, key := range strings.Split(c.Field, ".") {
		m, ok := got.(map[string]any)
		if !ok {
			return false
		}
		got = m[key]
	}
	if c.Op == "exists" {
		return got != nil
	}
	number := func(v any) (float64, bool) {
		switch n := v.(type) {
		case int:
			return float64(n), true
		case int64:
			return float64(n), true
		case float64:
			return n, true
		case json.Number:
			f, err := n.Float64()
			return f, err == nil
		default:
			return 0, false
		}
	}
	a, an := number(got)
	b, bn := number(c.Value)
	equal := reflect.DeepEqual(got, c.Value)
	if an && bn {
		equal = a == b
	}
	switch c.Op {
	case "eq":
		return equal
	case "ne":
		return !equal
	case "gt":
		return an && bn && a > b
	case "gte":
		return an && bn && a >= b
	case "lt":
		return an && bn && a < b
	case "lte":
		return an && bn && a <= b
	}
	return false
}
func Simulate(m Manifest, sequence string, vars map[string]any, start time.Time) ([]Trace, error) {
	if err := Validate(m); err != nil {
		return nil, err
	}
	var seq *Sequence
	for i := range m.Sequences {
		if m.Sequences[i].ID == sequence {
			seq = &m.Sequences[i]
			break
		}
	}
	if seq == nil {
		return nil, fmt.Errorf("unknown sequence %q", sequence)
	}
	at := start.UTC()
	steps := map[string]Step{}
	for _, s := range seq.Steps {
		steps[s.ID] = s
	}
	id := seq.Steps[0].ID
	traces := []Trace{}
	for count := 0; id != ""; count++ {
		if count >= 100 {
			return nil, fmt.Errorf("simulation step limit exceeded")
		}
		s, ok := steps[id]
		if !ok {
			return nil, fmt.Errorf("unknown step %q", id)
		}
		if seq.Exit != nil && Evaluate(seq.Exit, vars) {
			traces = append(traces, Trace{Step: id, Type: "exit", At: at, Reason: "exit condition matched"})
			break
		}
		tr := Trace{Step: id, Type: s.Type, At: at, Message: s.Message}
		next := s.Next
		switch s.Type {
		case "delay":
			d, _ := time.ParseDuration(s.Delay)
			at = at.Add(d)
			tr.Reason = "duration elapsed in UTC; timezone/DST changes do not alter elapsed delay"
		case "condition":
			match := Evaluate(s.Condition, vars)
			tr.Condition = &match
			if !match {
				next = s.Else
			}
		case "complete":
			next = ""
		}
		traces = append(traces, tr)
		id = next
	}
	return traces, nil
}
