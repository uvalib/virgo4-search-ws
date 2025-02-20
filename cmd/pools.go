package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/uvalib/virgo4-api/v4api"

	"github.com/gin-gonic/gin"
)

// this is a struct that mirrors the V4DB sources table
type source struct {
	ID         int
	PrivateURL string
	PublicURL  string
	Name       string
	Sequence   int
	Enabled    bool
}

// PoolsMiddleware sits after auth but before any other request. It checks for a sources param.
// If found it looks up all pools in that source set. If not found, the default pool set is looked up.
// Results placed in the request context for use by later handlers
func (svc *ServiceContext) PoolsMiddleware(c *gin.Context) {
	log.Printf("Pools Middleware: get pools")
	start := time.Now()
	pools, err := svc.lookupPools()
	if err != nil {
		log.Printf("ERROR: Unable to get pools: %+v", err)
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	log.Printf("SUCCESS: %d pools found in %dms", len(pools), elapsedMS)
	c.Set("pools", pools)
}

func getPoolsFromContext(c *gin.Context) []*pool {
	poolsIface, found := c.Get("pools")
	if !found {
		out := make([]*pool, 0)
		log.Printf("ERROR: No pools found")
		return out
	}
	return poolsIface.([]*pool)
}

// GetPoolsRequest gets a list of all active pools and returns it as JSON
func (svc *ServiceContext) GetPoolsRequest(c *gin.Context) {
	pools := getPoolsFromContext(c)
	out := make([]*poolResponse, 0)
	channel := make(chan *poolResponse)
	outstandingRequests := 0
	for _, p := range pools {
		outstandingRequests++
		go poolProviders(&p.V4ID, channel, svc.FastHTTPClient)
	}

	for outstandingRequests > 0 {
		poolResp := <-channel
		out = append(out, poolResp)
		outstandingRequests--
	}

	c.JSON(http.StatusOK, out)
}

// LookupPools fetches a list of current pools from the V4DB & pool /identify
// Any pools that fail the /identify call will not be included
func (svc *ServiceContext) lookupPools() ([]*pool, error) {
	var sources []*source
	log.Printf("INFO: lookup all pools")
	dbResp := svc.GDB.Debug().Where("sequence > ? and enabled=?", 0, true).Order("sequence asc").Find(&sources)
	if dbResp.Error != nil {
		log.Printf("ERROR: Unable to get authoritative pool information: %s", dbResp.Error.Error())
		return nil, dbResp.Error
	}

	channel := make(chan *identifyResult)
	outstandingRequests := 0
	for _, src := range sources {
		outstandingRequests++
		go identifyPool(src, channel, svc.FastHTTPClient)
	}

	pools := make([]*pool, 0)
	for outstandingRequests > 0 {
		idResp := <-channel
		if idResp.Error == nil {
			pools = append(pools, idResp.Pool)
		}
		outstandingRequests--
	}

	if len(pools) == 0 {
		log.Printf("ERROR: No pools found")
		return nil, errors.New("no pools found")
	}

	return pools, nil
}

type identifyResult struct {
	Pool  *pool
	Error error
}

// Goroutine to do a pool identify and return the results over a channel
func identifyPool(dbSrc *source, channel chan *identifyResult, httpClient *http.Client) {
	URL := fmt.Sprintf("%s/identify", dbSrc.PrivateURL)
	start := time.Now()
	identity := pool{PrivateURL: dbSrc.PrivateURL, Sequence: dbSrc.Sequence}

	log.Printf("INFO: request %s identity information from %s", dbSrc.Name, URL)
	idRequest, reqErr := http.NewRequest("GET", URL, nil)
	if reqErr != nil {
		log.Printf("ERROR: Unable to generate identify request for %s", URL)
		channel <- &identifyResult{Pool: nil, Error: fmt.Errorf("Unable to identify %s:%s", dbSrc.Name, dbSrc.PrivateURL)}
		return
	}
	resp, err := httpClient.Do(idRequest)
	if err != nil {
		log.Printf("ERROR: %s /identify failed: %s", dbSrc.PrivateURL, err.Error())
		channel <- &identifyResult{Pool: nil, Error: fmt.Errorf("Unable to identify %s:%s", dbSrc.Name, dbSrc.PrivateURL)}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("ERROR: %s/identify returned bad status code : %d: ", dbSrc.PrivateURL, resp.StatusCode)
		channel <- &identifyResult{Pool: nil, Error: fmt.Errorf("Unable to identify %s:%s", dbSrc.Name, dbSrc.PrivateURL)}
		return
	}

	respTxt, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(respTxt, &identity.V4ID)
	if err != nil {
		log.Printf("ERROR: Unable to parse response from %s: %s", dbSrc.PrivateURL, err.Error())
		channel <- &identifyResult{Pool: nil, Error: fmt.Errorf("Unable to identify %s:%s", dbSrc.Name, dbSrc.PrivateURL)}
		return
	}

	identity.V4ID.ID = dbSrc.Name
	identity.PrivateURL = dbSrc.PrivateURL
	identity.V4ID.URL = dbSrc.PublicURL
	for _, attr := range identity.V4ID.Attributes {
		if attr.Name == "external_hold" && attr.Supported == true {
			identity.IsExternal = true
			break
		}
	}
	poolsNS := time.Since(start)
	log.Printf("%s identified as %s. Time: %d ms", dbSrc.Name, identity.V4ID.Name, int64(poolsNS/time.Millisecond))
	channel <- &identifyResult{Pool: &identity, Error: nil}
}

// Goroutine to get pool providers, append them to pool data and return result
func poolProviders(pool *v4api.PoolIdentity, channel chan *poolResponse, httpClient *http.Client) {
	log.Printf("Get pool providers for %s", pool.ID)
	poolRes := poolResponse{PoolIdentity: pool}
	URL := fmt.Sprintf("%s/api/providers", pool.URL)
	provReq, reqErr := http.NewRequest("GET", URL, nil)
	if reqErr != nil {
		log.Printf("ERROR: Unable to generate identify request for %s", URL)
		channel <- &poolRes
		return
	}
	resp, err := httpClient.Do(provReq)
	if err != nil {
		log.Printf("ERROR: %s failed: %s", URL, err.Error())
		channel <- &poolRes
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("ERROR: %s returned bad status code : %d: ", URL, resp.StatusCode)
		channel <- &poolRes
		return
	}
	respTxt, _ := ioutil.ReadAll(resp.Body)
	var prov v4api.PoolProviders
	err = json.Unmarshal(respTxt, &prov)
	if err != nil {
		log.Printf("ERROR: %s returned invalid data: %s: ", URL, err.Error())
		channel <- &poolRes
		return
	}
	poolRes.Providers = &prov.Providers
	channel <- &poolRes
}
