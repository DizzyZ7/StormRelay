package incidents

import "testing"

func TestTransitions(t *testing.T) {
	tests := []struct {
		from, to State
		valid    bool
	}{
		{Detected, Acknowledged, true}, {Acknowledged, Resolved, true}, {Resolved, Reopened, true},
		{Closed, Acknowledged, false}, {Detected, Closed, false}, {Resolved, Detected, false},
	}
	for _, tt := range tests {
		err := ValidateTransition(tt.from, tt.to)
		if (err == nil) != tt.valid {
			t.Fatalf("%s -> %s: %v", tt.from, tt.to, err)
		}
	}
}
