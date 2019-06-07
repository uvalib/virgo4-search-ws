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

	"github.com/gin-gonic/gin"
	"github.com/uvalib/virgo4-parser/v4parser"
)

// Pagination cantains pagination info
type Pagination struct {
	Start int `json:"start"`
	Rows  int `json:"rows"`
	Total int `json:"total,omitempty"`
}

// PoolResult is the response from a single pool
type PoolResult struct {
	ServiceURL string                 `json:"service_url"`
	ElapsedMS  int64                  `json:"elapsed_ms,omitempty"`
	Pagination *Pagination            `json:"pagination"`
	Records    []*Record              `json:"record_list"`
	Confidence string                 `json:"confidence,omitempty"`
	Debug      map[string]interface{} `json:"debug"`
	Warnings   []string               `json:"warnings"`
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

// Record is a summary of one search hit
type Record struct {
	ID     string                 `json:"id"`
	Title  string                 `json:"title"`
	Author string                 `json:"author"`
	Debug  map[string]interface{} `json:"debug"`
}

// SearchPreferences contains preferences for the search
type SearchPreferences struct {
	TargetPool   string   `json:"target_pool"`
	ExcludePools []string `json:"exclude_pool"`
}

// IsExcluded will return true if the target URL is included in the ExcludePools preferece
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

// SearchRequest contains all of the data necessary for a client seatch request
type SearchRequest struct {
	Query       string             `json:"query"`
	Pagination  *Pagination        `json:"pagination"`
	Preferences *SearchPreferences `json:"preferences"`
}

// SearchResponse contains all search resonse data
type SearchResponse struct {
	Request       *SearchRequest    `json:"request"`
	PoolsSearched int               `json:"pools_searched"`
	TotalTimeMS   int64             `json:"total_time_ms"`
	TotalHits     int               `json:"total_hits"`
	Results       []*PoolResult     `json:"pool_results"`
	Debug         map[string]string `json:"debug"`
	Warnings      []string          `json:"warnings"`
}

// AsyncResponse is a wrapper around the data returned on a channel from the
// goroutine that gets search results from a pool
type AsyncResponse struct {
	PoolURL    string
	StatusCode int
	Message    string
	Results    *PoolResult
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
	if s.targetURL == s.results[j].ServiceURL {
		return false
	}
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
	var req SearchRequest
	if err := c.BindJSON(&req); err != nil {
		log.Printf("ERROR: unable to parse search request: %s", err.Error())
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("Search Request %+v", req)
	out := SearchResponse{Request: &req}

	valid, errors := v4parser.Validate(req.Query)
	if valid == false {
		log.Printf("ERROR: Query [%s] is not valid: %s", req.Query, errors)
		c.String(http.StatusBadRequest, errors)
		return
	}
	log.Printf("Query is valid")

	// see if target pool is also in exclude list
	if req.Preferences.IsExcluded(req.Preferences.TargetPool) {
		log.Printf("ERROR: Target Pool %s is also excluded", req.Preferences.TargetPool)
		c.String(http.StatusBadRequest, "Target pool cannot be excluded")
		return
	}

	if len(svc.Pools) == 0 {
		log.Printf("WARNING: No search pools registered")
		out.Warnings = append(out.Warnings, "No pools registered")
	}

	if svc.IsPoolRegistered(req.Preferences.TargetPool) {
		log.Printf("WARNING: Target Pool %s is not registered", req.Preferences.TargetPool)
		out.Warnings = append(out.Warnings, "Target pool is not active")
	}

	// Kick off all pool requests in parallel and wait for all to respond
	start := time.Now()
	channel := make(chan AsyncResponse)
	outstandingRequests := 0
	for _, p := range svc.Pools {
		if p.Alive == false {
			continue
		}
		if req.Preferences.IsExcluded(p.URL) {
			log.Printf("Skipping %s as it is part of the excluded URL list", p.URL)
			continue
		}
		outstandingRequests++
		out.PoolsSearched++
		go searchPool(p, req, channel)
	}

	// wait for all to be done and get respnses as they come in
	for outstandingRequests > 0 {
		asyncResult := <-channel
		if asyncResult.StatusCode == http.StatusOK {
			out.Results = append(out.Results, asyncResult.Results)
			out.TotalHits += asyncResult.Results.Pagination.Total
		} else {
			log.Printf("ERROR: %s returned %d:%s", asyncResult.PoolURL,
				asyncResult.StatusCode, asyncResult.Message)
		}
		outstandingRequests--
	}

	// Do a basic sort by tagetURL, confidence then hit count
	confidenceSort := byConfidence{results: out.Results, targetURL: req.Preferences.TargetPool}
	sort.Sort(&confidenceSort)

	// Total time for all respones (basically the longest response)
	elapsedNanoSec := time.Since(start)
	out.TotalTimeMS = int64(elapsedNanoSec / time.Millisecond)

	c.JSON(http.StatusOK, out)
}

// Goroutine to do a pool search and return the PoolResults on the channel
func searchPool(pool *Pool, req SearchRequest, channel chan AsyncResponse) {
	sURL := fmt.Sprintf("%s/api/search?debug=1", pool.URL)
	log.Printf("POST search to %s", sURL)
	respBytes, _ := json.Marshal(req)
	postReq, _ := http.NewRequest("POST", sURL, bytes.NewBuffer(respBytes))
	postReq.Header.Set("Content-Type", "application/json")
	timeout := time.Duration(15 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}

	start := time.Now()
	postResp, err := client.Do(postReq)
	elapsedNanoSec := time.Since(start)
	elapsedMS := int64(elapsedNanoSec / time.Millisecond)
	if err != nil {
		status := http.StatusBadRequest
		errMsg := err.Error()
		if strings.Contains(err.Error(), "Timeout") {
			status = http.StatusRequestTimeout
			errMsg = "request timed out"
		} else if strings.Contains(err.Error(), "connection refused") {
			status = http.StatusServiceUnavailable
			errMsg = "system is offline"
		}
		pool.Alive = false
		channel <- AsyncResponse{PoolURL: pool.URL, StatusCode: status, Message: errMsg}
		return
	}
	defer postResp.Body.Close()
	bodyBytes, _ := ioutil.ReadAll(postResp.Body)

	if postResp.StatusCode != 200 {
		channel <- AsyncResponse{PoolURL: pool.URL, StatusCode: postResp.StatusCode, Message: string(bodyBytes)}
		return
	}

	log.Printf("Successful pool response from %s. Elapsed Time: %dms", sURL, elapsedMS)
	log.Printf("RESPONSE: %s", string(bodyBytes))
	var poolResp PoolResult
	err = json.Unmarshal(bodyBytes, &poolResp)
	if err != nil {
		log.Printf("ERROR: Unable to parse pool response: %s", err.Error())
		channel <- AsyncResponse{PoolURL: pool.URL, StatusCode: http.StatusTeapot, Message: err.Error()}
		return
	}

	// Add elapsed time and stick it in the master search results format
	poolResp.ElapsedMS = elapsedMS
	channel <- AsyncResponse{PoolURL: pool.URL, StatusCode: http.StatusOK, Results: &poolResp}
}
