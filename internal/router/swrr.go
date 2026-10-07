package router

import "sync"

// swrrPicker implements smooth weighted round-robin (nginx-style) over the
// *current* eligible provider set. A provider that drops out of the candidate
// set does not participate in the current round, but its accumulated counter
// is retained (resumed when it becomes eligible again).
type swrrPicker struct {
	mu      sync.Mutex
	current map[string]int // provider id -> current weight
}

func newSWRRPicker() *swrrPicker {
	return &swrrPicker{current: make(map[string]int)}
}

// next picks the next candidate by weight. candidates must be non-empty.
func (s *swrrPicker) next(candidates []*ProviderEntry) *ProviderEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	for _, c := range candidates {
		id := c.Config.ID
		s.current[id] += c.Config.Weight
		total += c.Config.Weight
	}

	var best *ProviderEntry
	for _, c := range candidates {
		if best == nil || s.current[c.Config.ID] > s.current[best.Config.ID] {
			best = c
		}
	}
	if best == nil {
		return nil
	}
	s.current[best.Config.ID] -= total
	return best
}
