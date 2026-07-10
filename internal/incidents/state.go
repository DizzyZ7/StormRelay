package incidents

import "fmt"

type State string

const (
	Detected      State = "detected"
	Acknowledged  State = "acknowledged"
	Investigating State = "investigating"
	Mitigated     State = "mitigated"
	Resolved      State = "resolved"
	Closed        State = "closed"
	Reopened      State = "reopened"
)

var transitions = map[State]map[State]struct{}{
	Detected:      {Acknowledged: {}, Investigating: {}, Mitigated: {}, Resolved: {}},
	Acknowledged:  {Investigating: {}, Mitigated: {}, Resolved: {}},
	Investigating: {Mitigated: {}, Resolved: {}},
	Mitigated:     {Investigating: {}, Resolved: {}},
	Resolved:      {Closed: {}, Reopened: {}},
	Closed:        {Reopened: {}},
	Reopened:      {Acknowledged: {}, Investigating: {}, Mitigated: {}, Resolved: {}},
}

func ValidateTransition(from, to State) error {
	if from == to {
		return fmt.Errorf("incident is already %s", to)
	}
	if _, ok := transitions[from][to]; !ok {
		return fmt.Errorf("invalid incident transition %s -> %s", from, to)
	}
	return nil
}

func IsOpen(state State) bool { return state != Resolved && state != Closed }
