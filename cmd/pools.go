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

// Pool defines the attributes of a search pool. Pools are initially registered
// with on the ProvateURL. Full details are read from the /identify endpoint.
// PrivateURL should not be sent to client in json responses.
type Pool struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PrivateURL  string `json:"-"`
	PublicURL   string `json:"url"`
	Alive       bool   `json:"alive"`
}

// Identify will call the pool /identify endpoint to get full pool details.
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

// PoolExists checks if a pool with the given URL exists, regardless of the current status.
func (svc *ServiceContext) PoolExists(url string) bool {
	for _, p := range svc.Pools {
		if p.PrivateURL == url || p.PublicURL == url {
			return true
		}
	}
	return false
}

// IsPoolActive checks if a pool with the specified URL is registered and alive
func (svc *ServiceContext) IsPoolActive(url string) bool {
	for _, pool := range svc.Pools {
		if (pool.PrivateURL == url || pool.PublicURL == url) && pool.Alive {
			return true
		}
	}
	return false
}

// UpdateAuthoritativePools fetches a list of current pools from a DynamoDB. New pools
// will be added to an in-memory cache. If an existing pool is not found in the
// list, it will be removed from service.
func (svc *ServiceContext) UpdateAuthoritativePools() error {
	if svc.DevPoolsFile != "" {
		svc.LoadDevPools()
		return nil
	}
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
			log.Printf("Authoritative pools update found new pool URL %s", item.URL)
			pool := Pool{ID: fmt.Sprintf("%d", idx+1), PrivateURL: item.URL}
			svc.AddPool(pool)
		}
	}

	// Now see if there are any pools in memory that are no longer in the
	// authoritatve list, they have been retired and should be dropped
	svc.PrunePools(authoritativeURLs)
	return nil
}

// LoadDevPools is only used in local development mode. It will fetch a static list of
// pools from a text file. These pools will be pinged for health, but not updated.
func (svc *ServiceContext) LoadDevPools() {
	log.Printf("Load pools from dev mode pools file %s", svc.DevPoolsFile)
	data, _ := ioutil.ReadFile(svc.DevPoolsFile)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var authoritativeURLs []string
	for scanner.Scan() {
		svcURL := scanner.Text()
		authoritativeURLs = append(authoritativeURLs, svcURL)
		if svc.PoolExists(svcURL) {
			continue
		}
		log.Printf("Authoritative pools update found new pool URL %s", svcURL)
		pool := Pool{PrivateURL: svcURL}
		newID := len(svc.Pools) + 1
		pool.ID = fmt.Sprintf("%d", newID)
		svc.AddPool(pool)
	}
	svc.PrunePools(authoritativeURLs)
}

// AddPool will ping and identify a new pool. If both are successful, the pool as added to the in-memory
// list of available pools.
func (svc *ServiceContext) AddPool(pool Pool) {
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

// PrunePools compares the in-memory pools with the authoritative pool list. Any
// pools that are not on the authoritative list are removed.
func (svc *ServiceContext) PrunePools(authoritativeURLs []string) {
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
}
