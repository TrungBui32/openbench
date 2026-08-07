package runresult

import "testing"

// TestNewRunIDUnique guards against the previous minute-granularity format,
// where two runs of the same job within the same minute shared an ID (colliding
// container names, k8s job names, and terraform state keys).
func TestNewRunIDUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewRunID("driver-regression")
		if seen[id] {
			t.Fatalf("collision across 1000 draws: %s", id)
		}
		seen[id] = true
		if len(id) <= len("driver-regression") {
			t.Fatalf("run id lacks suffix: %s", id)
		}
	}
}
