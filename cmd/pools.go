package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/uvalib/virgo4-api/v4api"

	"github.com/gin-gonic/gin"
)

// PoolsMiddleware sits after auth but before any other request. It checks for a sources param.
// If found it looks up all pools in that source set. If not found, the default pool set is looked up.
// Results placed in the request context for use by later handlers
func (svc *ServiceContext) PoolsMiddleware(c *gin.Context) {
	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		acceptLang = "en-US"
	}

	log.Printf("Pools Middleware: get pools")
	start := time.Now()
	pools, err := svc.lookupPools(acceptLang)
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

// GetPoolsRequest gets a list of all active pools and returns it as JSON. This includes
// descriptive information localized to match the Accept-Language header. Fallback is en-US
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

// LookupPools fetches a localizes list of current pools the V4DB & pool /identify
// Any pools that fail the /identify call will not be included
func (svc *ServiceContext) lookupPools(language string) ([]*pool, error) {
	pools := make([]*pool, 0)
	q := svc.DB.NewQuery(`select * from sources where sequence>0 and enabled=true order by sequence asc`)
	rows, err := q.Rows()
	if err != nil {
		log.Printf("ERROR: Unable to get authoritative pool information: %+v", err)
		return nil, err
	}

	channel := make(chan *identifyResult)
	outstandingRequests := 0
	for rows.Next() {
		p := dbPool{}
		rows.ScanStruct(&p)
		outstandingRequests++
		go identifyPool(&p, language, channel, svc.FastHTTPClient)
	}

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
func identifyPool(dbp *dbPool, language string, channel chan *identifyResult, httpClient *http.Client) {
	URL := fmt.Sprintf("%s/identify", dbp.PrivateURL)
	languages := []string{language}
	if language != "en-US" {
		languages = append(languages, "en-US")
	}
	start := time.Now()
	identity := pool{PrivateURL: dbp.PrivateURL, Sequence: dbp.Sequence}
	identified := false
	for _, tgtLanguage := range languages {
		log.Printf("Request identity information from: %s in %s", URL, tgtLanguage)
		idRequest, reqErr := http.NewRequest("GET", URL, nil)
		if reqErr != nil {
			log.Printf("ERROR: Unable to generate identify request for %s", URL)
			continue
		}
		idRequest.Header.Set("Accept-Language", tgtLanguage)
		resp, err := httpClient.Do(idRequest)
		if err != nil {
			log.Printf("ERROR: %s /identify failed: %s", dbp.PrivateURL, err.Error())
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("ERROR: %s/identify returned bad status code : %d: ", dbp.PrivateURL, resp.StatusCode)
			continue
		}
		respTxt, _ := ioutil.ReadAll(resp.Body)
		err = json.Unmarshal(respTxt, &identity.V4ID)
		if err == nil {
			identity.V4ID.ID = dbp.Name
			identity.PrivateURL = dbp.PrivateURL
			identity.V4ID.URL = dbp.PublicURL
			for _, attr := range identity.V4ID.Attributes {
				if attr.Name == "external_hold" && attr.Supported == true {
					identity.IsExternal = true
					break
				}
			}
			poolsNS := time.Since(start)
			identified = true
			log.Printf("%s identified in %s as %s. Time: %d ms", dbp.Name, tgtLanguage, identity.V4ID.Name, int64(poolsNS/time.Millisecond))
			break
		} else {
			log.Printf("ERROR: Unable to parse response from %s: %s", dbp.PrivateURL, err.Error())
		}
	}

	if identified == false {
		errStr := fmt.Sprintf("Unable to identify %s:%s", dbp.Name, dbp.PrivateURL)
		channel <- &identifyResult{Pool: nil, Error: errors.New(errStr)}
	} else {
		channel <- &identifyResult{Pool: &identity, Error: nil}
	}
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
