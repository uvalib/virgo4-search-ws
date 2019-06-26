package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

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
		c.JSON(http.StatusOK, svc.Pools)
	}
}

// DeRegisterPool will remove a pool by URL passed in the request
func (svc *ServiceContext) DeRegisterPool(c *gin.Context) {
	tgtURL := c.Query("url")
	if tgtURL == "" {
		log.Printf("ERROR: missing url param")
		c.String(http.StatusBadRequest, "Missing required url param")
		return
	}

	var delPool *Pool
	poolIdx := -1
	for idx, p := range svc.Pools {
		if p.PrivateURL == tgtURL || p.PublicURL == tgtURL {
			delPool = p
			poolIdx = idx
			break
		}
	}
	if delPool == nil {
		log.Printf("ERROR: %s is not registered", tgtURL)
		c.String(http.StatusBadRequest, "%s is not registered", tgtURL)
		return
	}

	redisID := fmt.Sprintf("%s:pool:%s", svc.RedisPrefix, delPool.ID)
	_, err := svc.Redis.Del(redisID).Result()
	if err != nil {
		log.Printf("ERROR: Unable to delete %s : %s", tgtURL, err.Error())
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	servicesKey := fmt.Sprintf("%s:pools", svc.RedisPrefix)
	_, err = svc.Redis.SRem(servicesKey, delPool.ID).Result()
	if err != nil {
		log.Printf("ERROR: Unable to delete pool ID for %s : %s", tgtURL, err.Error())
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	svc.Pools = append(svc.Pools[:poolIdx], svc.Pools[poolIdx+1:]...)
	c.String(http.StatusOK, "unregistered %s", tgtURL)
}

// RegisterPool is called by a pool. It will be added to the list of
// pools that will be queried by  /search
func (svc *ServiceContext) RegisterPool(c *gin.Context) {
	type registration struct {
		Name string
		URL  string
	}
	var reg registration
	err := c.ShouldBindJSON(&reg)
	if err != nil {
		log.Printf("ERROR: register failed - %s", err.Error())
		c.String(http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("Received register for %+v", reg)
	pool := Pool{Name: reg.Name, PrivateURL: reg.URL}
	if err := pool.Ping(); err != nil {
		log.Printf("ERROR: New pool %s failed ping test", pool.PrivateURL)
		c.String(http.StatusBadRequest, "Failed ping test")
		return
	}

	// Grab some identify info from the pool API
	pool.Identify()

	// See if this pool already exists
	isNew := true
	for _, p := range svc.Pools {
		if p.PrivateURL == pool.PrivateURL {
			p.Alive = true
			isNew = false
			break
		}
	}

	if isNew == true {
		log.Printf("Registering new pool %s", pool.PrivateURL)
		// poolIDKey := fmt.Sprintf("%s:next_pool_id", svc.RedisPrefix)
		// newID, err := svc.Redis.Incr(poolIDKey).Result()
		// if err != nil {
		// 	log.Printf("ERROR: Unable to get ID new service: %s", err.Error())
		// 	c.String(http.StatusInternalServerError, err.Error())
		// 	return
		// }
		newID := len(svc.Pools) + 1
		pool.ID = fmt.Sprintf("%d", newID)
		// redisErr := svc.updateRedis(&pool, true)
		// if redisErr != nil {
		// 	log.Printf("Unable to get update redis %s", redisErr.Error())
		// 	c.String(http.StatusInternalServerError, redisErr.Error())
		// 	return
		// }
		svc.Pools = append(svc.Pools, &pool)
	}

	c.String(http.StatusOK, "registered")
}

// func (svc *ServiceContext) updateRedis(pool *Pool, newPool bool) error {
// 	redisID := fmt.Sprintf("%s:pool:%s", svc.RedisPrefix, pool.ID)
// 	_, err := svc.Redis.Set(redisID, pool.PrivateURL, 0).Result()
// 	if err != nil {
// 		return err
// 	}

// 	// This is a new pool.. add the ID to *:pools
// 	if newPool {
// 		servicesKey := fmt.Sprintf("%s:pools", svc.RedisPrefix)
// 		_, err = svc.Redis.SAdd(servicesKey, pool.ID).Result()
// 	}
// 	return err
// }
