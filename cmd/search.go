package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/gin-gonic/gin"
	"github.com/uvalib/virgo4-api/v4api"
	"github.com/uvalib/virgo4-parser/v4parser"
)

// Search queries all pools for results, collects and curates results. It will also send the query
// to the suggestor service and return suggested search terms. Response is JSON
func (svc *ServiceContext) Search(c *gin.Context) {
	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		acceptLang = "en-US"
	}
	localizer := i18n.NewLocalizer(svc.I18NBundle, acceptLang)

	var req clientSearchRequest
	if err := c.BindJSON(&req); err != nil {
		log.Printf("ERROR: Unable to parse search request: %s", err.Error())
		err := searchError{Message: localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "BadSearch"}),
			Details: err.Error()}
		c.JSON(http.StatusBadRequest, err)
		return
	}
	log.Printf("Search Request %+v with Accept-Language %s", req, acceptLang)

	valid, errors := v4parser.ValidateWithTimeout(req.Query, 10)
	if valid == false {
		log.Printf("ERROR: Query [%s] is not valid: %s", req.Query, errors)
		err := searchError{Message: localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "BadSearch"}),
			Details: errors}
		c.JSON(http.StatusBadRequest, err)
		return
	}

	// Pools have already been placed in request context by poolsMiddleware. Get them or fail
	pools := getPoolsFromContext(c)
	if len(pools) == 0 {
		err := searchError{Message: localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "NoPools"}),
			Details: errors}
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	// Do the search...
	out := NewSearchResponse(&req)
	start := time.Now()
	if req.Preferences.TargetPool != "" && PoolExists(req.Preferences.TargetPool, pools) == false {
		log.Printf("WARNING: Target Pool %s is not registered", req.Preferences.TargetPool)
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "TargetPoolUnavailable"})
		out.Warnings = append(out.Warnings, msg)
	}

	// headers to send to pool
	headers := map[string]string{
		"Content-Type":    "application/json",
		"Accept-Language": acceptLang,
		"Authorization":   c.GetHeader("Authorization"),
	}

	sugChannel := make(chan []v4api.Suggestion)
	sugURL := fmt.Sprintf("%s/api/suggest", svc.SuggestorURL)
	go svc.getSuggestions(sugURL, req.Query, headers, sugChannel)

	// Kick off all pool requests in parallel and wait for all to respond
	channel := make(chan *v4api.PoolResult)
	outstandingRequests := 0
	for _, p := range pools {
		out.Pools = append(out.Pools, p.V4ID)

		if isPoolExcluded(&req.Preferences, p) {
			log.Printf("Skipping %s as it is part of the excluded pools list", p.V4ID.URL)
			continue
		}
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
		log.Printf("Pool %s has %d hits and status %d[%s]", poolResponse.ServiceURL,
			poolResponse.Pagination.Total, poolResponse.StatusCode, poolResponse.StatusMessage)
		if poolResponse.StatusCode == http.StatusOK {
			out.TotalHits += poolResponse.Pagination.Total
		} else {
			logLevel := "ERROR"
			// we want to log "not implemented" differently as they are "expected" in some cases
			// (some pools do not support some query types, etc)
			// this ensures the log filters pick up real errors
			if poolResponse.StatusCode == http.StatusNotImplemented {
				logLevel = "WARNING"
			}
			log.Printf("%s: %s returned %d:%s", logLevel, poolResponse.ServiceURL,
				poolResponse.StatusCode, poolResponse.StatusMessage)
			out.Warnings = append(out.Warnings, poolResponse.StatusMessage)
		}
		outstandingRequests--
	}

	out.Suggestions = <-sugChannel

	// sort pool results
	// poolSort := byName{results: out.Results}
	if c.Query("sources") != "default" {
		poolSort := bySequence{results: out.Results, pools: pools}
		sort.Sort(&poolSort)
	} else {
		poolSort := byConfidence{results: out.Results, targetURL: req.Preferences.TargetPool}
		sort.Sort(&poolSort)
	}

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
	sURL := fmt.Sprintf("%s/api/search", pool.PrivateURL)

	// only send filter group applicable to this pool (if any)
	poolReq := req
	poolReq.Filters = []v4api.Filter{}
	poolReq.Sort = v4api.SortOrder{SortID: "SortRelevance", Order: "desc"}

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

func isPoolExcluded(searchPrefs *v4api.SearchPreferences, pool *pool) bool {
	for _, identifier := range searchPrefs.ExcludePools {
		if identifier == pool.V4ID.URL || identifier == pool.V4ID.ID {
			return true
		}
	}
	return false
}
