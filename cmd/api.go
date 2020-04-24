package main

import "github.com/uvalib/virgo4-api/v4api"

// this is a struct that mirrors the V4DB sources table
type dbPool struct {
	ID         int    `json:"-" db:"id"`
	PrivateURL string `json:"-" db:"private_url"`
	PublicURL  string `json:"-" db:"public_url"`
	Name       string `json:"-" db:"name"`
}

// pool is an extension of the API pool which includes private URL
type pool struct {
	V4ID       v4api.PoolIdentity
	PrivateURL string `json:"-"`
}

// NewSearchResponse creates a new instance of a search response
func NewSearchResponse(req *v4api.SearchRequest) *v4api.SearchResponse {
	return &v4api.SearchResponse{Request: req,
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
