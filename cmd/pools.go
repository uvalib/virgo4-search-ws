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

	"github.com/gin-gonic/gin"
)

// Pool defines the attributes of a search pool. Pools are initially registered
// with PrivateURL and an internal name. Full details are read from the /identify endpoint.
type Pool struct {
	ID          int             `json:"-" db:"id"`
	Name        string          `json:"id" db:"name"`
	PrivateURL  string          `json:"-" db:"private_url"`
	PublicURL   string          `json:"url" db:"public_url"`
	Language    string          `json:"-"`
	DisplayName string          `json:"name"`
	Description string          `json:"description"`
	Attributes  []PoolAttribute `json:"attributes,omitempty"`
}

// PoolAttribute defines a sungle attribute that a pool may support
type PoolAttribute struct {
	Name      string `json:"name"`
	Supported bool   `json:"supported"`
	Value     string `json:"value,omitempty"`
}

// Identify will call the pool /identify endpoint to get full pool details in the target language
// If a translation for the target language cannot be found, return en-US  if possible.
func (p *Pool) Identify(language string) error {
	type idResp struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Mode        string          `json:"mode,omitempty"`
		Attributes  []PoolAttribute `json:"attributes,omitempty"`
	}
	timeout := time.Duration(2 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}
	URL := fmt.Sprintf("%s/identify", p.PrivateURL)
	languages := []string{language, "en-US"}
	start := time.Now()
	for _, tgtLanguage := range languages {
		log.Printf("Request identity information from: %s", URL)
		idRequest, reqErr := http.NewRequest("GET", URL, nil)
		if reqErr != nil {
			log.Printf("ERROR: Unable to generate identify request for %s", URL)
			continue
		}
		idRequest.Header.Set("Accept-Language", tgtLanguage)
		resp, err := client.Do(idRequest)
		if err != nil {
			log.Printf("ERROR: %s /identify failed: %s", p.PrivateURL, err.Error())
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("ERROR: %s/identify returned bad status code : %d: ", p.PrivateURL, resp.StatusCode)
			continue
		}

		var identity idResp
		respTxt, _ := ioutil.ReadAll(resp.Body)
		json.Unmarshal(respTxt, &identity)
		p.Language = tgtLanguage
		p.DisplayName = identity.Name
		p.Description = identity.Description
		p.Attributes = identity.Attributes
		poolsNS := time.Since(start)
		log.Printf("%s identified in %s as %s. Time: %d ms", p.Name, p.Language, p.DisplayName, int64(poolsNS/time.Millisecond))
		return nil
	}

	p.DisplayName = p.Name
	p.PublicURL = p.PrivateURL
	errStr := fmt.Sprintf("Unable to identify %s:%s", p.Name, p.PrivateURL)
	return errors.New(errStr)
}

// GetPoolsRequest gets a list of all active pools and returns it as JSON. This includes
// descriptive information localized to match the Accept-Language header. Fallback is en-US
func (svc *ServiceContext) GetPoolsRequest(c *gin.Context) {
	// Pick the first option in Accept-Language header - or en-US if none
	acceptLang := strings.Split(c.GetHeader("Accept-Language"), ",")[0]
	log.Printf("GetPools Accept-Language %s", acceptLang)
	if acceptLang == "" {
		acceptLang = "en-US"
	}

	pools, err := svc.LookupPools(acceptLang)
	if err != nil {
		log.Printf("ERROR: GetPools failed %+v", err)
		c.String(http.StatusInternalServerError, err.Error())
	}

	c.JSON(http.StatusOK, pools)
}

// PoolExists checks if a pool with the given URL exists, regardless of the current status.
func PoolExists(url string, pools []*Pool) bool {
	for _, p := range pools {
		if p.PrivateURL == url || p.PublicURL == url {
			return true
		}
	}
	return false
}

// LookupPools fetches a localizez list of current pools the V4DB & pool /identify
// Any pools that fail the /identify call will not be included
func (svc *ServiceContext) LookupPools(language string) ([]*Pool, error) {
	pools := make([]*Pool, 0, 0)
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
		p := Pool{}
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
	Pool  *Pool
	Error error
}

// Goroutine to do a pool identify and return the results over a channel
func identifyPool(pool *Pool, language string, channel chan *identifyResult) {
	err := pool.Identify(language)
	channel <- &identifyResult{Pool: pool, Error: err}
}
