package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Sort cantains data to define search sort order
type Sort struct {
	Field string `json:"field"`
	Order string `json:"order"`
}

// Query defines the search terms and order
type Query struct {
	Keyword string `json:"keyword,omitempty"`
	Author  string `json:"author,omitempty"`
	Title   string `json:"title,omitempty"`
	Subject string `json:"subject,omitempty"`
	Sort    *Sort  `json:"sort"`
}

// Pagination cantains pagination info
type Pagination struct {
	Start int `json:"start"`
	Rows  int `json:"rows"`
	Total int `json:"total,omitempty"`
}

// Preferences cantains search preferences
type Preferences struct {
	DefaultPool  string   `json:"default_search_pool"`
	ExcludePools []string `json:"excluded_pools"`
}

// PoolResults is the response from a single pool
type PoolResults struct {
	PoolID     string      `json:"pool_id"`
	ServiceURL string      `json:"service_url"`
	Summary    string      `json:"summary"`
	ElapsedMS  int64       `json:"elapsed_ms"`
	Pagination *Pagination `json:"pagination"`
	Records    []*Record   `json:"record_list"`
	Filters    string      `json:"filters,omitempty"`
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
	Query       *Query       `json:"query"`
	Pagination  *Pagination  `json:"pagination"`
	Preferences *Preferences `json:"search_preferences"`
}

// SearchResponse contains all search resonse data
type SearchResponse struct {
	ActualRequest    *SearchRequest `json:"actual_request"`
	EffectiveRequest *SearchRequest `json:"summary,omitempty"`
	Results          []*PoolResults `json:"pool_results"`
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

	defaultPool := "catalog"
	if req.Preferences != nil {
		defaultPool = req.Preferences.DefaultPool
		if defaultPool == "" {
			defaultPool = "catalog"
		}
	}
	log.Printf("Default search pool: %s", defaultPool)

	var tgtPool *Pool
	for _, p := range svc.Pools {
		if p.Alive == false {
			continue
		}
		if p.Name == defaultPool {
			log.Printf("Found default pool %s, searching...", p.Name)
			tgtPool = p
		}
	}

	if tgtPool == nil {
		log.Printf("ERROR: default search pool %s not found", defaultPool)
		c.String(http.StatusBadRequest, "Pool %s not found", defaultPool)
		return
	}

	sURL := fmt.Sprintf("%s/api/search", tgtPool.URL)
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
		log.Printf("ERROR: Pool query POST to %s failed: %s", sURL, err.Error())
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	defer postResp.Body.Close()
	bodyBytes, _ := ioutil.ReadAll(postResp.Body)

	if postResp.StatusCode != 200 {
		log.Printf("ERROR: Pool query to %s FAILED with status %d:%s. Elapsed Time: %dms",
			sURL, postResp.StatusCode, bodyBytes, elapsedMS)
		c.String(postResp.StatusCode, string(bodyBytes))
		return
	}

	log.Printf("Successful pool response from %s. Elapsed Time: %dms", sURL, elapsedMS)
	log.Printf("RESPONSE: %s", string(bodyBytes))
	type poolResp struct {
		Results []*PoolResults `json:"results_pools"`
	}
	var rawResp poolResp
	err = json.Unmarshal(bodyBytes, &rawResp)
	if err != nil {
		log.Printf("ERROR: Unable to parse pool response: %s", err.Error())
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// Add elapsed time and stick it in the master search results format
	pr := rawResp.Results[0]
	pr.ElapsedMS = elapsedMS
	out := SearchResponse{ActualRequest: &req}
	out.Results = append(out.Results, pr)

	c.JSON(http.StatusOK, out)
}
