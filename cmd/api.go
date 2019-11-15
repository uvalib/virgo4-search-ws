package main

// SearchRequest contains all of the data necessary for a client seatch request
type SearchRequest struct {
	Query       string            `json:"query"`
	Pagination  Pagination        `json:"pagination"`
	Facet       string            `json:"facet"`
	Filters     []VirgoFilter     `json:"filters"`
	Preferences SearchPreferences `json:"preferences"`
}

// SearchQP defines the query params that could be passed to the pools
type SearchQP struct {
	debug  string
	intuit string
}

// VirgoFilter contains the fields for a single filter.
type VirgoFilter struct {
	FacetID string `json:"facet_id"`
	Value   string `json:"value"`
}

// SearchResponse contains all search resonse data
type SearchResponse struct {
	Request     *SearchRequest `json:"request"`
	Pools       []*Pool        `json:"pools"`
	TotalTimeMS int64          `json:"total_time_ms"`
	TotalHits   int            `json:"total_hits"`
	Results     []*PoolResult  `json:"pool_results"`
	Warnings    []string       `json:"warnings"`
}

// Pagination cantains pagination info
type Pagination struct {
	Start int `json:"start"`
	Rows  int `json:"rows"`
	Total int `json:"total"`
}

// PoolResult is the response from a single pool
type PoolResult struct {
	ServiceURL      string                 `json:"service_url,omitempty"`
	PoolName        string                 `json:"pool_id,omitempty"`
	ElapsedMS       int64                  `json:"elapsed_ms,omitempty"`
	Pagination      Pagination             `json:"pagination"`
	Records         []Record               `json:"record_list,omitempty"`
	Groups          []Group                `json:"group_list,omitempty"`
	AvailableFacets []VirgoFacet           `json:"available_facets"`     // available facets advertised to the client
	FacetList       []VirgoFacet           `json:"facet_list,omitempty"` // facet values for client-requested facets
	DefaultFacets   []VirgoDefaultFacet    `json:"default_facets,omitempty"`
	Confidence      string                 `json:"confidence,omitempty"`
	Debug           map[string]interface{} `json:"debug"`
	Warnings        []string               `json:"warnings"`
	StatusCode      int                    `json:"status_code"`
	StatusMessage   string                 `json:"status_msg,omitempty"`
	ContentLanguage string                 `json:"-"`
}

// VirgoFacet contains the fields for a single facet.
type VirgoFacet struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Buckets []VirgoFacetBucket `json:"buckets,omitempty"`
}

// VirgoFacetBucket contains the fields for an individual bucket for a facet.
type VirgoFacetBucket struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// VirgoDefaultFacet contains fields for a default facet.
// This format would also work for a more general SelectedFacet if needed
type VirgoDefaultFacet struct {
	ID     string   `json:"facet_id"`
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Record is a summary of one search hit
type Record struct {
	Fields []RecordField          `json:"fields"`
	Debug  map[string]interface{} `json:"debug"`
}

// Group contains the records for a single group in a search result set.
type Group struct {
	Value   string        `json:"value"`
	Count   int           `json:"count"`
	Fields  []RecordField `json:"fields,omitempty"`
	Records []Record      `json:"record_list,omitempty"`
}

// RecordField contains metadata for a single field in a record.
type RecordField struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"` // empty implies "text"
	Label      string `json:"label"`
	Value      string `json:"value"`
	Visibility string `json:"visibility,omitempty"` // e.g. "basic" or "detailed".  empty implies "basic"
	Display    string `json:"display,omitempty"`    // e.g. "optional".  empty implies not optional
}

// SearchPreferences contains preferences for the search
type SearchPreferences struct {
	TargetPool   string   `json:"target_pool"`
	ExcludePools []string `json:"exclude_pool"`
}

// NewSearchResponse creates a new instance of a search response
func NewSearchResponse(req *SearchRequest) *SearchResponse {
	return &SearchResponse{Request: req,
		Pools:    make([]*Pool, 0),
		Results:  make([]*PoolResult, 0),
		Warnings: make([]string, 0, 0),
	}
}

// NewPoolResult creates a new result struct
func NewPoolResult(pool *Pool, ms int64) *PoolResult {
	return &PoolResult{ServiceURL: pool.PublicURL, PoolName: pool.Name,
		ElapsedMS: ms, Warnings: make([]string, 0, 0), AvailableFacets: make([]VirgoFacet, 0, 0),
	}
}
