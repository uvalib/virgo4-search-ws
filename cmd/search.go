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
	// confidence is equal; sort by hit count
	return s.results[i].Pagination.Total > s.results[j].Pagination.Total
}

// Search queries all pools for results, collects and curates results. It will also send the query
// to the suggestor service and return suggested search terms. Response is JSON
func (svc *ServiceContext) Search(c *gin.Context) {
	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		acceptLang = "en-US"
	}
	localizer := i18n.NewLocalizer(svc.I18NBundle, acceptLang)

	var req v4api.SearchRequest
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

	// Just before each search, lookup the pools to search
	out := NewSearchResponse(&req)
	start := time.Now()
	pools, err := svc.lookupPools(acceptLang)
	if err != nil {
		log.Printf("ERROR: unable to get search pools: %+v", err)
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "NoPools"})
		c.String(http.StatusInternalServerError, msg)
		return
	}
	log.Printf("Search %d pools", len(pools))

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
	go getSuggestions(sugURL, req.Query, headers, sugChannel)

	// Kick off all pool requests in parallel and wait for all to respond
	channel := make(chan *v4api.PoolResult)
	outstandingRequests := 0
	for _, p := range pools {
		out.Pools = append(out.Pools, p.V4ID)

		// NOTE: the client only knows about publicURL so all excludes
		// will be done with it as the key
		if req.Preferences.IsExcluded(p.V4ID.URL) {
			log.Printf("Skipping %s as it is part of the excluded URL list", p.V4ID.URL)
			continue
		}
		outstandingRequests++
		go searchPool(p, req, c.Query("debug"), headers, channel)
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

	out.Suggestions = <-sugChannel

	// sort pool results
	// poolSort := byName{results: out.Results}
	poolSort := byConfidence{results: out.Results, targetURL: req.Preferences.TargetPool}
	sort.Sort(&poolSort)

	// Total time for all respones (basically the longest response)
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	out.TotalTimeMS = elapsedMS

	log.Printf("Received all pool responses. Elapsed Time: %d (ms)", elapsedMS)
	c.Header("Content-Language", contentLanguage)
	c.JSON(http.StatusOK, out)
}

func getSuggestions(url string, query string, headers map[string]string, channel chan []v4api.Suggestion) {
	var reqStruct struct {
		Query string
	}
	reqStruct.Query = query
	reqBytes, _ := json.Marshal(reqStruct)
	resp := servicePost(url, reqBytes, headers)
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
		log.Printf("ERROR: malformed suggestor response: %s", err.Error())
		channel <- make([]v4api.Suggestion, 0)
		return
	}

	channel <- respStruct.Suggestions
}

// Goroutine to do a pool search and return the PoolResults on the channel
func searchPool(pool *pool, req v4api.SearchRequest, debug string, headers map[string]string, channel chan *v4api.PoolResult) {
	// Master search always uses the Private URL to communicate with pools
	sURL := fmt.Sprintf("%s/api/search?debug=%s", pool.PrivateURL, debug)

	// only send filter group applicable to this pool (if any)
	poolReq := req
	poolReq.Filters = []v4api.Filter{}

	for _, filterGroup := range req.Filters {
		if filterGroup.PoolID == pool.V4ID.ID {
			poolReq.Filters = append(poolReq.Filters, filterGroup)
			break
		}
	}

	reqBytes, _ := json.Marshal(poolReq)
	postResp := servicePost(sURL, reqBytes, headers)
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

	if results.Pagination == nil {
		results.Pagination = &v4api.Pagination{Start: 0, Total: 0, Rows: 0}
	}

	// If we are this far, there is a valid response. Add language
	results.StatusCode = http.StatusOK
	results.ElapsedMS = postResp.ElapsedMS
	results.ContentLanguage = postResp.ContentLanguage

	channel <- results
}
