package main

import "github.com/uvalib/virgo4-api/v4api"

// this is a struct that mirrors the V4DB sources table
type dbPool struct {
	ID         int    `json:"-" db:"id"`
	PrivateURL string `json:"-" db:"private_url"`
	PublicURL  string `json:"-" db:"public_url"`
	Name       string `json:"-" db:"name"`
	Sequence   int    `json:"-" db:"sequence"`
}

// pool is an extension of the API pool which includes private URL
// and an easy access flag to indicate if the pool is external (like JRML & WorldCat)
type pool struct {
	V4ID       v4api.PoolIdentity
	PrivateURL string `json:"-"`
	IsExternal bool   `json:"-"`
	Sequence   int    `json:"-"`
}

type poolSort struct {
	PoolID string          `json:"poolID"`
	Sort   v4api.SortOrder `json:"sort"`
}

type clientSearchRequest struct {
	v4api.SearchRequest
	PoolSort []poolSort `json:"pool_sorting"`
}

// MasterResponse is the search-ws response to a search request. It is different from the
// API SearchResponse in that it includes modified client request that includes an array of
// pool sort options
type MasterResponse struct {
	Request     *clientSearchRequest `json:"request"`
	Pools       []v4api.PoolIdentity `json:"pools"`
	TotalTimeMS int64                `json:"total_time_ms"`
	TotalHits   int                  `json:"total_hits"`
	Results     []*v4api.PoolResult  `json:"pool_results"`
	Warnings    []string             `json:"warnings"`
	Suggestions []v4api.Suggestion   `json:"suggestions"`
}

// NewSearchResponse creates a new instance of a search response
func NewSearchResponse(req *clientSearchRequest) *MasterResponse {
	return &MasterResponse{Request: req,
		Pools:    make([]v4api.PoolIdentity, 0),
		Results:  make([]*v4api.PoolResult, 0),
		Warnings: make([]string, 0),
	}
}

// NewPoolResult creates a new result struct
func NewPoolResult(pool *pool, ms int64) *v4api.PoolResult {
	return &v4api.PoolResult{ServiceURL: pool.V4ID.URL, PoolName: pool.V4ID.ID,
		ElapsedMS: ms, Warnings: make([]string, 0),
	}
}

type searchError struct {
	Message string `json:"message"`
	Details string `json:"details"`
}
