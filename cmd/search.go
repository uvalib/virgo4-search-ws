package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/uvalib/virgo4-api/v4api"
	"github.com/uvalib/virgo4-parser/v4parser"
)

// Search queries all pools for results, collects and curates results. It will also send the query
// to the suggestor service and return suggested search terms. Response is JSON
func (svc *ServiceContext) Search(c *gin.Context) {
	var req clientSearchRequest
	if jsonErr := c.BindJSON(&req); jsonErr != nil {
		log.Printf("ERROR: Unable to parse search request: %s", jsonErr.Error())
		err := searchError{Message: "This query is malformed or unsupported.", Details: jsonErr.Error()}
		c.JSON(http.StatusBadRequest, err)
		return
	}

	valid, errors := v4parser.Validate(req.Query)
	if valid == false {
		log.Printf("ERROR: Query [%s] is not valid: %s", req.Query, errors)
		err := searchError{Message: "This query is malformed or unsupported.", Details: errors}
		c.JSON(http.StatusBadRequest, err)
		return
	}

	// Pools have already been placed in request context by poolsMiddleware. Get them or fail
	pools := getPoolsFromContext(c)
	if len(pools) == 0 {
		err := searchError{Message: "All resourcess are surrently offline. Please try again later.", Details: errors}
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	// headers to send to pool
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": c.GetHeader("Authorization"),
	}

	// kick off a request to get suggestions based on search query
	sugChannel := make(chan []v4api.Suggestion)
	sugURL := fmt.Sprintf("%s/api/suggest", svc.SuggestorURL)
	go svc.getSuggestions(sugURL, req.Query, headers, sugChannel)

	// Do the search...
	out := NewSearchResponse(&req)
	start := time.Now()
	channel := make(chan *v4api.PoolResult)
	outstandingRequests := 0
	for _, p := range pools {
		out.Pools = append(out.Pools, p.V4ID)
		outstandingRequests++
		go svc.searchPool(p, req, headers, channel)
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
		log.Printf("Pool %s has %d hits and status %d [%s]", poolResponse.ServiceURL,
			poolResponse.Pagination.Total, poolResponse.StatusCode, poolResponse.StatusMessage)
		if poolResponse.StatusCode == http.StatusOK {
			out.TotalHits += poolResponse.Pagination.Total
		} else {
			logLevel := "ERROR"
			// We want to log "not implemented" differently as they are "expected" in some cases
			// (some pools do not support some query types, etc.)
			// This ensures the log filters pick up real errors
			// Also pool timeouts are considered warnings cos we are adding a special filter
			// to track them independently
			if poolResponse.StatusCode == http.StatusNotImplemented || poolResponse.StatusCode == http.StatusRequestTimeout {
				logLevel = "WARNING"
			}
			log.Printf("%s: %s returned %d:%s", logLevel, poolResponse.ServiceURL,
				poolResponse.StatusCode, poolResponse.StatusMessage)
			out.Warnings = append(out.Warnings, poolResponse.StatusMessage)
		}
		outstandingRequests--
	}

	out.Suggestions = <-sugChannel

	// sort pool results by pool sequence
	log.Printf("Sort results by sequence")
	poolSort := bySequence{results: out.Results, pools: pools}
	sort.Sort(&poolSort)

	// Total time for all respones (basically the longest response)
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	out.TotalTimeMS = elapsedMS

	log.Printf("Received all pool responses. Elapsed Time: %d (ms)", elapsedMS)
	c.Header("Content-Language", contentLanguage)
	c.JSON(http.StatusOK, out)
}

func (svc *ServiceContext) getSuggestions(url string, query string, headers map[string]string, channel chan []v4api.Suggestion) {
	var reqStruct struct {
		Query string
	}
	reqStruct.Query = query
	reqBytes, _ := json.Marshal(reqStruct)
	resp := serviceRequest("POST", url, reqBytes, headers, svc.HTTPClient)
	if resp.StatusCode != http.StatusOK {
		channel <- make([]v4api.Suggestion, 0)
		return
	}

	log.Printf("Raw suggestor response: %s", resp.Response)
	var respStruct struct {
		Suggestions []v4api.Suggestion
	}
	err := json.Unmarshal(resp.Response, &respStruct)
	if err != nil {
		log.Printf("ERROR: Malformed suggestor response: %s", err.Error())
		channel <- make([]v4api.Suggestion, 0)
		return
	}

	channel <- respStruct.Suggestions
}

// Goroutine to do a pool search and return the PoolResults on the channel
func (svc *ServiceContext) searchPool(pool *pool, req clientSearchRequest, headers map[string]string, channel chan *v4api.PoolResult) {
	// Master search always uses the Private URL to communicate with pools
	// NOTE: Sending the debug QP to get max_score info from each pool
	sURL := fmt.Sprintf("%s/api/search?debug=1", pool.PrivateURL)

	// only send filter group applicable to this pool (if any)
	poolReq := req
	poolReq.Filters = []v4api.Filter{}

	log.Printf("INFO: lookup starting sort order for %s", pool.V4ID.ID)
	poolReq.Sort = v4api.SortOrder{SortID: "SortRelevance", Order: "desc"}
	for _, s := range req.PoolSort {
		if s.PoolID == pool.V4ID.ID {
			log.Printf("INFO: pool %s starting sort: %+v", pool.V4ID.ID, s.Sort)
			poolReq.Sort = s.Sort
		}
	}

	for _, filterGroup := range req.Filters {
		if filterGroup.PoolID == pool.V4ID.ID {
			poolReq.Filters = append(poolReq.Filters, filterGroup)
			break
		}
	}
	for _, poolSort := range req.PoolSort {
		if poolSort.PoolID == pool.V4ID.ID {
			poolReq.Sort = poolSort.Sort
			break
		}
	}

	reqBytes, _ := json.Marshal(poolReq)
	httpClient := svc.HTTPClient
	if pool.IsExternal {
		log.Printf("Pool %s is managed externally, reduce timeout to 5 seconds", pool.V4ID.Name)
		httpClient = svc.FastHTTPClient
	}
	postResp := serviceRequest("POST", sURL, reqBytes, headers, httpClient)
	results := NewPoolResult(pool, postResp.ElapsedMS)
	if postResp.StatusCode != http.StatusOK {
		results.StatusCode = postResp.StatusCode
		results.StatusMessage = string(postResp.Response)
		channel <- results
		return
	}

	err := json.Unmarshal(postResp.Response, results)
	if err != nil {
		results.StatusCode = http.StatusInternalServerError
		results.StatusMessage = "Malformed search response"
		channel <- results
		return
	}

	// If we are this far, there is a valid response. Add language
	results.StatusCode = http.StatusOK
	results.ElapsedMS = postResp.ElapsedMS
	results.ContentLanguage = postResp.ContentLanguage

	channel <- results
}
