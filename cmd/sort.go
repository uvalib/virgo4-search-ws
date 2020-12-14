package main

import (
	"github.com/uvalib/virgo4-api/v4api"
)

// bySequence will sort responses by pool set sequence number
type bySequence struct {
	results []*v4api.PoolResult
	pools   []*pool
}

func (s *bySequence) Len() int {
	return len(s.results)
}

func (s *bySequence) Swap(i, j int) {
	s.results[i], s.results[j] = s.results[j], s.results[i]
}

func (s *bySequence) Less(i, j int) bool {
	var a *pool
	var b *pool
	for _, p := range s.pools {
		if p.V4ID.ID == s.results[i].PoolName {
			a = p
		}
		if p.V4ID.ID == s.results[j].PoolName {
			b = p
		}
	}
	if a == nil || b == nil {
		return false
	}
	return a.Sequence < b.Sequence
}
