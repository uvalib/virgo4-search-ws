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

// byName will sort responses by name
type byName struct {
	results []*PoolResult
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

// Search queries all pools for results, collects and curates results. Responds with JSON.
func (svc *ServiceContext) Search(c *gin.Context) {
	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		acceptLang = "en-US"
	}
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

	// Just before each search, lookup the pools to search
	out := NewSearchResponse(&req)
	start := time.Now()
	pools, err := svc.LookupPools(acceptLang)
	if err != nil {
		log.Printf("ERROR: unable to get search pools: %+v", err)
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "NoPools"})
		c.String(http.StatusInternalServerError, msg)
		return
	}
	log.Printf("Search %d pools", len(pools))
	out.Pools = pools

	if req.Preferences.TargetPool != "" && PoolExists(req.Preferences.TargetPool, pools) == false {
		log.Printf("WARNING: Target Pool %s is not registered", req.Preferences.TargetPool)
		msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "TargetPoolUnavailable"})
		out.Warnings = append(out.Warnings, msg)
	}

	// grab QP config for debug, search intuit, etc.
	qp := SearchQP{debug: c.Query("debug"), intuit: c.Query("intuit")}

	// headers to send to pool
	headers := map[string]string{
		"Content-Type":    "application/json",
		"Accept-Language": acceptLang,
		"Authorization":   c.GetHeader("Authorization"),
	}

	// Kick off all pool requests in parallel and wait for all to respond
	channel := make(chan *PoolResult)
	outstandingRequests := 0
	for _, p := range out.Pools {
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

	// sort pools by name
	nameSort := byName{results: out.Results}
	sort.Sort(&nameSort)

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
	sURL := fmt.Sprintf("%s/api/search?debug=%s&intuit=%s", pool.PrivateURL, qp.debug, qp.intuit)
	log.Printf("POST search to %s", sURL)
	reqBytes, _ := json.Marshal(req)
	postReq, _ := http.NewRequest("POST", sURL, bytes.NewBuffer(reqBytes))

	for name, val := range headers {
		postReq.Header.Set(name, val)
	}

	timeout := time.Duration(10 * time.Second)
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
