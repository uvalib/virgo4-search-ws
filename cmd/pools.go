package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	dbx "github.com/go-ozzo/ozzo-dbx"
	"github.com/uvalib/virgo4-api/v4api"

	"github.com/gin-gonic/gin"
)

// PoolsMiddleware sits after auth but before any other request. It checks for a sources param.
// If found it looks up all pools in that source set. If not found, the default pool set is looked up.
// Results placed in the request context for use by later handlers
func (svc *ServiceContext) PoolsMiddleware(c *gin.Context) {
	srcSet := c.Query("sources")
	if srcSet == "" {
		srcSet = "default"
	}

	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		acceptLang = "en-US"
	}

	log.Printf("Pools Middleware: get %s pools", srcSet)
	start := time.Now()
	pools, err := svc.lookupPools(acceptLang, srcSet)
	if err != nil {
		log.Printf("ERROR: Unable to get %s pools: %+v", srcSet, err)
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	log.Printf("SUCCESS: %d %s pools found in %dms", len(pools), srcSet, elapsedMS)
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
	out := make([]v4api.PoolIdentity, 0)
	for _, p := range pools {
		out = append(out, p.V4ID)
	}

	c.JSON(http.StatusOK, out)
}

// PoolExists checks if a pool with the given identifier (URL or Name)
func PoolExists(identifier string, pools []*pool) bool {
	for _, p := range pools {
		if p.V4ID.URL == identifier || p.V4ID.ID == identifier {
			return true
		}
	}
	return false
}

// LookupPools fetches a localizes list of current pools the V4DB & pool /identify
// Any pools that fail the /identify call will not be included
func (svc *ServiceContext) lookupPools(language string, srcSet string) ([]*pool, error) {
	pools := make([]*pool, 0)
	q := svc.DB.NewQuery(` select s.*, t.sequence from sources s inner join source_sets t on t.source_id=s.id where t.name={:set} and s.enabled=true order by t.sequence asc`)
	q.Bind(dbx.Params{"set": srcSet})
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
