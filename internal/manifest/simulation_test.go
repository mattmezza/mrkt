package manifest

import (
	"reflect"
	"testing"
	"time"
)

func TestDeterministicSimulation(t *testing.T) {
	m := valid()
	m.Sequences[0].Steps = []Step{{ID: "branch", Type: "condition", Condition: &Condition{Field: "Event.total", Op: "gte", Value: 10}, Next: "wait", Else: "done"}, {ID: "wait", Type: "delay", Delay: "24h", Next: "send"}, {ID: "send", Type: "send", Message: "welcome", Next: "done"}, {ID: "done", Type: "complete"}}
	vars := map[string]any{"Event": map[string]any{"total": 15.0}}
	start := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	a, err := Simulate(m, "welcome", vars, start)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Simulate(m, "welcome", vars, start)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("simulation non-deterministic")
	}
	if len(a) != 4 || a[2].At.Sub(start) != 24*time.Hour {
		t.Fatal(a)
	}
	if Evaluate(&Condition{Field: "Event.total", Op: "eq", Value: "15"}, vars) {
		t.Fatal("coerced string to numeric")
	}
	m.Sequences[0].Exit = &Condition{Field: "Event.total", Op: "gt", Value: 10}
	a, err = Simulate(m, "welcome", vars, start)
	if err != nil || len(a) != 1 || a[0].Type != "exit" {
		t.Fatal(a, err)
	}
}
