package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/gin-gonic/gin"
	"github.com/uvalib/virgo4-parser/v4parser"
)

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
	debug   string
	intuit  string
	grouped string
}

// VirgoFilter contains the fields for a single filter.
type VirgoFilter struct {
	FacetID string `json:"facet_id"`
	Value   string `json:"value"`
}

// NewSearchResponse creates a new instance of a search response
func NewSearchResponse(req *SearchRequest) *SearchResponse {
	return &SearchResponse{Request: req,
		Results:  make([]*PoolResult, 0),
		Warnings: make([]string, 0, 0),
	}
}

// SearchResponse contains all search resonse data
type SearchResponse struct {
	Request     *SearchRequest      `json:"request"`
	Pools       []LocalizedPoolInfo `json:"pools"`
	TotalTimeMS int64               `json:"total_time_ms"`
	TotalHits   int                 `json:"total_hits"`
	Results     []*PoolResult       `json:"pool_results"`
	Warnings    []string            `json:"warnings"`
}

// Pagination cantains pagination info
type Pagination struct {
	Start int `json:"start"`
	Rows  int `json:"rows"`
	Total int `json:"total"`
}

// NewPoolResult creates a new result struct
func NewPoolResult(pool *Pool, ms int64) *PoolResult {
	return &PoolResult{ServiceURL: pool.PublicURL, PoolName: pool.Name,
		ElapsedMS: ms, ContentLanguage: pool.FallbackLanguage,
		Warnings: make([]string, 0, 0), AvailableFacets: make([]VirgoFacet, 0, 0),
	}
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

// Record is a summary of one search hit
type Record struct {
	Fields []RecordField          `json:"fields"`
	Debug  map[string]interface{} `json:"debug"`
}

// Group contains the records for a single group in a search result set.
type Group struct {
	Value   string   `json:"value"`
	Count   int      `json:"count"`
	Records []Record `json:"record_list,omitempty"`
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

// ConfidenceIndex will convert a string confidence into a numeric value
// with low having the lowest value and exaxt the highest
func (pr *PoolResult) ConfidenceIndex() int {
	conf := []string{"low", "medium", "high", "exact"}
	for idx, val := range conf {
		if val == pr.Confidence {
			return idx
		}
	}
	// No confidence match. Assume worst value
	return 0
}

// IsExcluded will return true if the target URL is included in the ExcludePools preferece
// Note that the URL passed is always the Public URL, as this is the only one the client knows about
func (p *SearchPreferences) IsExcluded(URL string) bool {
	if URL == "" {
		return false
	}
	for _, excludedURL := range p.ExcludePools {
		if excludedURL == URL {
			return true
		}
	}
	return false
}

// byConfidence will sort responses by confidence, then hit count
// If a target pool is specified, it will put that one first
type byConfidence struct {
	results   []*PoolResult
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
	// confidence is equal; sort by hit count
	return s.results[i].Pagination.Total > s.results[j].Pagination.Total
}

// Search queries all pools for results, collects and curates results. Responds with JSON.
func (svc *ServiceContext) Search(c *gin.Context) {
	acceptLang := c.GetHeader("Accept-Language")
	localizer := i18n.NewLocalizer(svc.I18NBundle, acceptLang)
	var req SearchRequest
	if err := c.BindJSON(&req); err != nil {
		log.Printf("ERROR: unable to parse search request: %s", err.Error())
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "BadSearch"})
		c.String(http.StatusBadRequest, msg)
		return
	}
	log.Printf("Search Request %+v with Accept-Language %s", req, acceptLang)

	valid, errors := v4parser.Validate(req.Query)
	if valid == false {
		log.Printf("ERROR: Query [%s] is not valid: %s", req.Query, errors)
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "BadSearch"})
		c.String(http.StatusBadRequest, msg)
		return
	}

	// see if target pool is also in exclude list
	if req.Preferences.TargetPool != "" && req.Preferences.IsExcluded(req.Preferences.TargetPool) {
		log.Printf("ERROR: Target Pool %s is also excluded", req.Preferences.TargetPool)
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "TargetExcluded"})
		c.String(http.StatusBadRequest, msg)
		return
	}

	// Just before each search, check the authoritative pool list
	// and see if any new pools have been added, or pools have been retired.
	out := NewSearchResponse(&req)
	start := time.Now()
	log.Printf("Pre-search, pre-update pools count %d", len(svc.Pools))
	svc.UpdateAuthoritativePools()
	if len(svc.Pools) == 0 {
		log.Printf("ERROR: No search pools registered")
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "NoPools"})
		c.String(http.StatusInternalServerError, msg)
		return
	}
	log.Printf("Pre-search, post-update pools count %d", len(svc.Pools))

	// Get all public pool info in the language of the client request
	out.Pools = svc.GetLocalizedPoolInfo(acceptLang)

	if req.Preferences.TargetPool != "" && svc.IsPoolActive(req.Preferences.TargetPool) == false {
		log.Printf("WARNING: Target Pool %s is not registered", req.Preferences.TargetPool)
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "TargetPoolUnavailable"})
		out.Warnings = append(out.Warnings, msg)
	}

	// grab QP config for debug or search intuit
	qp := SearchQP{debug: c.Query("debug"), intuit: c.Query("intuit"), grouped: c.Query("grouped")}

	// headers to send to pool
	headers := map[string]string{
		"Content-Type":    "application/json",
		"Accept-Language": acceptLang,
		"Authorization":   c.GetHeader("Authorization"),
	}

	// Kick off all pool requests in parallel and wait for all to respond
	channel := make(chan *PoolResult)
	outstandingRequests := 0
	for _, p := range svc.Pools {
		if p.Alive == false {
			if p.Ping() != nil {
				log.Printf("Skipping %s as it is not alive", p.PublicURL)
				continue
			}
		}

		// NOTE: the client only knows about publicURL so all excludes
		// will be done with it as the key
		if req.Preferences.IsExcluded(p.PublicURL) {
			log.Printf("Skipping %s as it is part of the excluded URL list", p.PublicURL)
			continue
		}
		outstandingRequests++
		go searchPool(p, req, qp, headers, channel)
	}

	// wait for all to be done and get respnses as they come in
	var contentLanguage string
	for outstandingRequests > 0 {
		poolResponse := <-channel
		out.Results = append(out.Results, poolResponse)
		if contentLanguage == "" {
			contentLanguage = poolResponse.ContentLanguage
			log.Printf("Set response Content-Language to %s", contentLanguage)
		}
		log.Printf("Pool %s has %d hits and status %d[%s]", poolResponse.ServiceURL,
			poolResponse.Pagination.Total, poolResponse.StatusCode, poolResponse.StatusMessage)
		if poolResponse.StatusCode == http.StatusOK {
			out.TotalHits += poolResponse.Pagination.Total
		} else {
			log.Printf("ERROR: %s returned %d:%s", poolResponse.ServiceURL,
				poolResponse.StatusCode, poolResponse.StatusMessage)
			out.Warnings = append(out.Warnings, poolResponse.StatusMessage)
		}
		outstandingRequests--
	}

	// Do a basic sort by tagetURL, confidence then hit count
	confidenceSort := byConfidence{results: out.Results, targetURL: req.Preferences.TargetPool}
	sort.Sort(&confidenceSort)

	// Total time for all respones (basically the longest response)
	elapsedNanoSec := time.Since(start)
	elapsedMS := int64(elapsedNanoSec / time.Millisecond)
	out.TotalTimeMS = elapsedMS

	log.Printf("Received all pool responses. Elapsed Time: %d (ms)", elapsedMS)
	c.Header("Content-Language", contentLanguage)
	c.JSON(http.StatusOK, out)
}

// Goroutine to do a pool search and return the PoolResults on the channel
func searchPool(pool *Pool, req SearchRequest, qp SearchQP, headers map[string]string, channel chan *PoolResult) {
	// Master search always uses the Private URL to communicate with pools
	sURL := fmt.Sprintf("%s/api/search?debug=%s&intuit=%s&grouped=%s", pool.PrivateURL, qp.debug, qp.intuit, qp.grouped)
	log.Printf("POST search to %s", sURL)
	respBytes, _ := json.Marshal(req)
	postReq, _ := http.NewRequest("POST", sURL, bytes.NewBuffer(respBytes))

	for name, val := range headers {
		postReq.Header.Set(name, val)
	}

	timeout := time.Duration(5 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}

	start := time.Now()
	postResp, err := client.Do(postReq)
	elapsedNanoSec := time.Since(start)
	elapsedMS := int64(elapsedNanoSec / time.Millisecond)
	results := NewPoolResult(pool, elapsedMS)
	if err != nil {
		status := http.StatusBadRequest
		errMsg := err.Error()
		if strings.Contains(err.Error(), "Timeout") {
			status = http.StatusRequestTimeout
			errMsg = fmt.Sprintf("%s search timed out", pool.PrivateURL)
		} else if strings.Contains(err.Error(), "connection refused") {
			status = http.StatusServiceUnavailable
			errMsg = fmt.Sprintf("%s is offline", pool.PrivateURL)
		}
		pool.Alive = false
		results.StatusCode = status
		results.StatusMessage = errMsg
		channel <- results
		return
	}

	defer postResp.Body.Close()
	bodyBytes, _ := ioutil.ReadAll(postResp.Body)
	if postResp.StatusCode != 200 {
		results.StatusCode = postResp.StatusCode
		results.StatusMessage = string(bodyBytes)
		channel <- results
		return
	}

	err = json.Unmarshal(bodyBytes, results)
	if err != nil {
		results.StatusCode = http.StatusInternalServerError
		results.StatusMessage = "Malformed search response"
		channel <- results
		return
	}

	// If we are this far, there is a valid response. Add language
	results.StatusCode = http.StatusOK
	elapsedNanoSec = time.Since(start)
	results.ElapsedMS = int64(elapsedNanoSec / time.Millisecond)
	results.ContentLanguage = postResp.Header.Get("Content-Language")
	if results.ContentLanguage == "" {
		results.ContentLanguage = postReq.Header.Get("Accept-Language")
	}

	log.Printf("Successful pool response from %s. Elapsed Time: %d (ms)", sURL, elapsedMS)
	channel <- results
}
