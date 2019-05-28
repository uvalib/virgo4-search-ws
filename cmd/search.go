package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Pagination cantains pagination info
type Pagination struct {
	Start int `json:"start"`
	Rows  int `json:"rows"`
	Total int `json:"total,omitempty"`
}

// PoolResult is the response from a single pool
type PoolResult struct {
	ServiceURL string      `json:"service_url"`
	ElapsedMS  int64       `json:"elapsed_ms,omitempty"`
	Pagination *Pagination `json:"pagination"`
	Records    []*Record   `json:"record_list"`
	Confidence string      `json:"confidence,omitempty"`
}

// Record is a summary of one search hit
type Record struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Author string `json:"author"`
}

// SearchRequest contains all of the data necessary for a client seatch request
type SearchRequest struct {
	Query      string      `json:"query"`
	Pagination *Pagination `json:"pagination"`
}

// SearchResponse contains all search resonse data
type SearchResponse struct {
	Request       *SearchRequest `json:"request"`
	PoolsSearched int            `json:"pools_searched"`
	TotalTimeMS   int64          `json:"total_time_ms"`
	TotalHits     int            `json:"total_hits"`
	Results       []*PoolResult  `json:"pool_results"`
}

// AsyncResponse is a wrapper around the data returned on a channel from the
// goroutine that gets search results from a pool
type AsyncResponse struct {
	PoolURL    string
	StatusCode int
	Message    string
	Results    *PoolResult
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
	if len(svc.Pools) == 0 {
		log.Printf("ERROR: No search pools registered")
		c.String(http.StatusInternalServerError, "No search pools available")
		return
	}

	// Kick off all pool requests in parallel and wait for all to respond
	out := SearchResponse{Request: &req}
	start := time.Now()
	channel := make(chan AsyncResponse)
	outstandingRequests := 0
	for _, p := range svc.Pools {
		if p.Alive == false {
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

	// Total time for all respones (basically the longest response)
	elapsedNanoSec := time.Since(start)
	out.TotalTimeMS = int64(elapsedNanoSec / time.Millisecond)

	c.JSON(http.StatusOK, out)
}

// Goroutine to do a pool search and return the PoolResults on the channel
func searchPool(pool *Pool, req SearchRequest, channel chan AsyncResponse) {
	sURL := fmt.Sprintf("%s/api/search", pool.URL)
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
