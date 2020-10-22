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

// byName will sort responses by name
type byName struct {
	results []*v4api.PoolResult
}

func (s *byName) Len() int {
	return len(s.results)
}

func (s *byName) Swap(i, j int) {
	s.results[i], s.results[j] = s.results[j], s.results[i]
}

func (s *byName) Less(i, j int) bool {
	return s.results[i].PoolName < s.results[j].PoolName
}

// byConfidence will sort responses by confidence, then hit count
// If a target pool is specified, it will put that one first
type byConfidence struct {
	results   []*v4api.PoolResult
	targetURL string
}

func (s *byConfidence) Len() int {
	return len(s.results)
}

func (s *byConfidence) Swap(i, j int) {
	s.results[i], s.results[j] = s.results[j], s.results[i]
}

func (s *byConfidence) Less(i, j int) bool {
	// bubble matching URL to top
	if s.targetURL == s.results[i].ServiceURL {
		return true
	}
	if s.targetURL == s.results[j].ServiceURL {
		return false
	}
	// sort by confidence index
	if s.results[i].ConfidenceIndex() < s.results[j].ConfidenceIndex() {
		return false
	}
	if s.results[i].ConfidenceIndex() > s.results[j].ConfidenceIndex() {
		return true
	}

	// confidence is equal; sort by top score in results
	maxScoreI, _ := s.results[i].Debug["max_score"].(float64)
	maxScoreJ, _ := s.results[j].Debug["max_score"].(float64)
	if maxScoreI < maxScoreJ {
		return false
	}

	if maxScoreI > maxScoreJ {
		return true
	}

	// last effort, go by total results
	return s.results[i].Pagination.Total > s.results[j].Pagination.Total
}
