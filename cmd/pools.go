package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/gin-gonic/gin"
)

// Pool defines the attributes of a search pool
// At registration, the pool sends its private URL. Next,
// /identify is called to get the publicURL. Only the public
// should be sent to client in json responses as 'url.
// Private is omitted with json:"-"
type Pool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PrivateURL  string `json:"-"`
	PublicURL   string `json:"url"`
	Alive       bool   `json:"alive"`
}

// Identify will call the pool /identify endpoint and add descriptive info to the pool
func (p *Pool) Identify() bool {
	timeout := time.Duration(5 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}
	URL := fmt.Sprintf("%s/identify", p.PrivateURL)
	resp, err := client.Get(URL)
	if err != nil {
		log.Printf("ERROR: %s /identify failed: %s", p.PrivateURL, err.Error())
		p.Alive = false
		return false
	}

	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("   * FAIL: %s/identify returned bad status code : %d: ", p.PrivateURL, resp.StatusCode)
		p.Alive = false
		return false
	}

	type idResp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		PublicURL   string `json:"public_url"`
	}
	var identity idResp
	respTxt, _ := ioutil.ReadAll(resp.Body)
	json.Unmarshal(respTxt, &identity)
	p.Name = identity.Name
	p.Description = identity.Description
	p.PublicURL = identity.PublicURL
	return true
}

// Ping will check the health of a pool by calling /healthcheck and looking for good status
func (p *Pool) Ping() error {
	timeout := time.Duration(5 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}
	hcURL := fmt.Sprintf("%s/healthcheck", p.PrivateURL)
	resp, err := client.Get(hcURL)
	if err != nil {
		log.Printf("ERROR: %s ping failed: %s", p.PrivateURL, err.Error())
		p.Alive = false
		return err
	}

	defer resp.Body.Close()
	respTxt, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("   * FAIL: %s returned bad status code : %d: ", p.PrivateURL, resp.StatusCode)
		p.Alive = false
		return fmt.Errorf("%d:%s", resp.StatusCode, respTxt)
	}

	if strings.Contains(string(respTxt), "false") {
		log.Printf("   * FAIL: %s has unhealthy components", p.PrivateURL)
		p.Alive = false
		return fmt.Errorf("%s", respTxt)
	}

	p.Alive = true
	return nil
}

// GetPools gets a list of all active pools and returns it as JSON
func (svc *ServiceContext) GetPools(c *gin.Context) {
	if len(svc.Pools) == 0 {
		c.JSON(http.StatusOK, make([]*Pool, 0, 1))
	} else {
		// only return those pools that are listed as alive.
		// others have errors and are not currently in use
		active := make([]*Pool, 0)
		for _, p := range svc.Pools {
			if p.Alive {
				active = append(active, p)
			}
		}
		c.JSON(http.StatusOK, active)
	}
}

// PingPools checks health of all attached pools and updates their status accordingly
func (svc *ServiceContext) PingPools() {
	log.Printf("Checking %d pools for health", len(svc.Pools))
	for _, p := range svc.Pools {
		if err := p.Ping(); err != nil {
			log.Printf("   * %s offline: %s", p.PrivateURL, err.Error())
		}
	}
}

// PoolExists checks if a pool with the given URL exists
func (svc *ServiceContext) PoolExists(url string) bool {
	for _, p := range svc.Pools {
		if p.PrivateURL == url || p.PublicURL == url {
			return true
		}
	}
	return false
}

// UpdateAuthoritativePools fetches a list of current pools from a DynamoDB. New pools
// will be added to an in-memory cache. If an existing pool is not found in the
// list, it will be removed from service.
func (svc *ServiceContext) UpdateAuthoritativePools() error {
	log.Printf("Scanning for pool updates in %s", svc.PoolsTable)
	params := dynamodb.ScanInput{
		TableName: aws.String(svc.PoolsTable),
	}
	result, err := svc.DynamoDB.Scan(&params)
	if err != nil {
		log.Printf("ERROR: Unable to retrieve pools from AWS: %v", err)
		return err
	}

	// NOTE: This structure matches the only attribute value in the DynamoDB table
	type Item struct {
		URL string
	}
	var authoritativeURLs []string
	for idx, ddbItem := range result.Items {
		item := Item{}
		err = dynamodbattribute.UnmarshalMap(ddbItem, &item)
		if err != nil {
			log.Printf("Unable to read DDB item %v: %v", ddbItem, err)
		} else {
			authoritativeURLs = append(authoritativeURLs, item.URL)
			if svc.PoolExists(item.URL) {
				// pool already exists; no nothing
				continue
			}
			pool := Pool{ID: fmt.Sprintf("%d", idx+1), PrivateURL: item.URL}
			if err := pool.Ping(); err != nil {
				log.Printf("   * %s is not available: %s", pool.PrivateURL, err.Error())
			} else {
				if pool.Identify() {
					log.Printf("   * %s is alive and identified %s", pool.PrivateURL, pool.Name)
					svc.Pools = append(svc.Pools, &pool)
				} else {
					log.Printf("   * %s is alive, but failed identify. Skipping", pool.PrivateURL)
				}
			}
		}
	}

	// Now see if there are any pools in memory that are no longer in the
	// authoritatve list, they have been retired and should be dropped
	for idx := len(svc.Pools) - 1; idx >= 0; idx-- {
		p := svc.Pools[idx]
		found := false
		for _, authURL := range authoritativeURLs {
			if authURL == p.PrivateURL || authURL == p.PublicURL {
				found = true
				break
			}
		}
		if found == false {
			log.Printf("Pool %s:%s is no longer on the authoritative list. Removing", p.Name, p.PrivateURL)
			svc.Pools = append(svc.Pools[:idx], svc.Pools[idx+1:]...)
		}
	}
	return nil
}

// LoadDevPools is only used in local development mode. It will fetch a static list of
// pools from a text file. These pools will be pinged for health, but not updated.
func (svc *ServiceContext) LoadDevPools(cfgFile string) {
	log.Printf("Using dev mode pools file %s", cfgFile)
	data, _ := ioutil.ReadFile(cfgFile)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		svcURL := scanner.Text()
		pool := Pool{PrivateURL: svcURL}
		newID := len(svc.Pools) + 1
		pool.ID = fmt.Sprintf("%d", newID)
		if err := pool.Ping(); err != nil {
			log.Printf("   * %s is not available: %s", pool.PrivateURL, err.Error())
		} else {
			log.Printf("   * %s is alive", pool.PrivateURL)
			pool.Identify()
			log.Printf("Pool identified as %+v", pool)
			svc.Pools = append(svc.Pools, &pool)
		}
	}
}
