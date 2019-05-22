package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pool defines the attributes of a search pool
type Pool struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Alive bool   `json:"alive"`
}

// Ping will check the health of a pool by calling /healthcheck and looking for good status
func (p *Pool) Ping() bool {
	timeout := time.Duration(5 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}
	hcURL := fmt.Sprintf("%s/healthcheck", p.URL)
	resp, err := client.Get(hcURL)
	if err != nil {
		log.Printf("ERROR: Pool %s ping failed: %s", p.Name, err.Error())
		p.Alive = false
		return false
	}

	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("   * FAIL: Pool %s returned bad status code : %d: ", p.Name, resp.StatusCode)
		p.Alive = false
		return false
	}

	// read response and make sure it contains the name of the service
	respTxt, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("   * FAIL: Pool %s returned unreadable response : %s: ", p.Name, err.Error())
		p.Alive = false
		return false
	}

	// parse response into a map with string key and value
	parsed := make(map[string]string)
	err = json.Unmarshal([]byte(respTxt), &parsed)
	if err != nil {
		log.Printf("   * FAIL: Pool %s returned invalid response : %s: ", p.Name, err.Error())
		return false
	}

	// Walk the values and look for any 'false'. Fail if found
	for key, val := range parsed {
		if val != "true" {
			log.Printf("   * FAIL: Pool %s has failed component %s", p.Name, key)
		}
	}

	p.Alive = true
	return true
}

// GetPools gets a list of all active pools and returns it as JSON
func (svc *ServiceContext) GetPools(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not yet implemented")
}

// RegisterPool is called by a pool. It will be added to the list of
// pools that will be queried by  /search
func (svc *ServiceContext) RegisterPool(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not yet implemented")
}
