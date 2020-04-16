package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/uvalib/virgo4-api/v4api"

	"github.com/gin-gonic/gin"
)

// GetPoolsRequest gets a list of all active pools and returns it as JSON. This includes
// descriptive information localized to match the Accept-Language header. Fallback is en-US
func (svc *ServiceContext) GetPoolsRequest(c *gin.Context) {
	// Pick the first option in Accept-Language header - or en-US if none
	acceptLang := strings.Split(c.GetHeader("Accept-Language"), ",")[0]
	log.Printf("GetPools Accept-Language %s", acceptLang)
	if acceptLang == "" {
		acceptLang = "en-US"
	}

	pools, err := svc.lookupPools(acceptLang)
	if err != nil {
		log.Printf("ERROR: GetPools failed %+v", err)
		c.String(http.StatusInternalServerError, err.Error())
	}
	out := make([]v4api.PoolIdentity, 0)
	for _, p := range pools {
		out = append(out, p.V4ID)
	}

	c.JSON(http.StatusOK, out)
}

// PoolExists checks if a pool with the given URL exists, regardless of the current status.
func PoolExists(url string, pools []*pool) bool {
	for _, p := range pools {
		if p.V4ID.URL == url {
			return true
		}
	}
	return false
}

// LookupPools fetches a localizes list of current pools the V4DB & pool /identify
// Any pools that fail the /identify call will not be included
func (svc *ServiceContext) lookupPools(language string) ([]*pool, error) {
	pools := make([]*pool, 0, 0)
	q := svc.DB.NewQuery(`select * from sources`)
	rows, err := q.Rows()
	if err != nil {
		log.Printf("ERROR: Unable to get authoritative pool information: %+v", err)
		return nil, err
	}

	start := time.Now()
	channel := make(chan *identifyResult)
	outstandingRequests := 0
	for rows.Next() {
		p := dbPool{}
		rows.ScanStruct(&p)
		outstandingRequests++
		go identifyPool(&p, language, channel)
	}

	for outstandingRequests > 0 {
		idResp := <-channel
		if idResp.Error == nil {
			pools = append(pools, idResp.Pool)
		}
		outstandingRequests--
	}

	poolsNS := time.Since(start)
	log.Printf("Time to identify %d pools %dMS", len(pools), int64(poolsNS/time.Millisecond))

	if len(pools) == 0 {
		log.Printf("ERROR: No pools found")
		return nil, errors.New("No pools found")
	}

	return pools, nil
}

type identifyResult struct {
	Pool  *pool
	Error error
}

// Goroutine to do a pool identify and return the results over a channel
func identifyPool(dbp *dbPool, language string, channel chan *identifyResult) {
	timeout := time.Duration(2 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}
	URL := fmt.Sprintf("%s/identify", dbp.PrivateURL)
	languages := []string{language}
	if language != "en-US" {
		languages = append(languages, "en-US")
	}
	start := time.Now()
	identity := pool{PrivateURL: dbp.PrivateURL}
	identified := false
	for _, tgtLanguage := range languages {
		log.Printf("Request identity information from: %s in %s", URL, tgtLanguage)
		idRequest, reqErr := http.NewRequest("GET", URL, nil)
		if reqErr != nil {
			log.Printf("ERROR: Unable to generate identify request for %s", URL)
			continue
		}
		idRequest.Header.Set("Accept-Language", tgtLanguage)
		resp, err := client.Do(idRequest)
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
