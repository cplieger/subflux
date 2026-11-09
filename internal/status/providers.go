package status

import "time"

// Health is a provider's state.
type Health uint8

// The provider states; the zero Health is none of them.
const (
	OK Health = iota + 1
	Degraded
	Disabled
)

// ProviderState is one provider's health, until when it holds, and why.
type ProviderState struct {
	Until  time.Time
	ID     string
	Reason string
	Health Health
}

// SetProvider records p, its Reason cut as an alert's text is. Once
// RetainProviders has run, a p whose ID it did not name is ignored.
func (s *Store) SetProvider(p ProviderState) {
	p.Reason = cutText(p.Reason)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.liveProviders != nil && !s.liveProviders[p.ID] {
		return
	}
	if cur, ok := s.providers[p.ID]; ok && cur.Health == p.Health && cur.Reason == p.Reason && cur.Until.Equal(p.Until) {
		return
	}
	s.providers[p.ID] = p
	s.bump()
}

// RetainProviders drops every provider state whose ID is not in live, and
// from then on SetProvider ignores one outside live, so a removed provider's
// late call cannot bring it back. It signals observers once, and only when it
// drops a state. app calls it after each activation with the active
// providers' ids.
func (s *Store) RetainProviders(live []string) {
	set := make(map[string]bool, len(live))
	for _, id := range live {
		set[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveProviders = set
	dropped := false
	for id := range s.providers {
		if !set[id] {
			delete(s.providers, id)
			dropped = true
		}
	}
	if dropped {
		s.bump()
	}
}
