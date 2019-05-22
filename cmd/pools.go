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
	Name  string `json:"name" binding:"required"`
	URL   string `json:"url" binding:"required"`
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
	c.JSON(http.StatusOK, svc.Pools)
}

// RegisterPool is called by a pool. It will be added to the list of
// pools that will be queried by  /search
func (svc *ServiceContext) RegisterPool(c *gin.Context) {
	var pool Pool
	err := c.ShouldBindJSON(&pool)
	if err != nil {
		log.Printf("ERROR: register failed - %s", err.Error())
		c.String(http.StatusBadRequest, err.Error())
		return
	}

	log.Printf("Received register for %+v", pool)
	if pool.Ping() == false {
		log.Printf("ERROR: New pool %s:%s failed ping test", pool.Name, pool.URL)
		c.String(http.StatusBadRequest, "Failed ping test")
		return
	}

	// See if this pool already exists
	isNew := true
	for _, p := range svc.Pools {
		if p.Name == pool.Name {
			p.URL = pool.URL
			p.Alive = true
			isNew = false
			break
		}
	}

	if isNew == true {
		log.Printf("Registering new pool %+v", pool)
		poolIDKey := fmt.Sprintf("%s:next_pool_id", svc.RedisPrefix)
		newID, err := svc.Redis.Incr(poolIDKey).Result()
		if err != nil {
			log.Printf("ERROR: Unable to get ID new service: %s", err.Error())
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		pool.ID = fmt.Sprintf("%d", newID)
		redisErr := svc.updateRedis(&pool, true)
		if redisErr != nil {
			log.Printf("Unable to get update redis %s", redisErr.Error())
			c.String(http.StatusInternalServerError, redisErr.Error())
			return
		}
		svc.Pools = append(svc.Pools, &pool)
	}

	c.String(http.StatusOK, "registered")
}

func (svc *ServiceContext) updateRedis(pool *Pool, newPool bool) error {
	redisID := fmt.Sprintf("%s:pool:%s", svc.RedisPrefix, pool.ID)
	_, err := svc.Redis.HMSet(redisID, map[string]interface{}{
		"id":   pool.ID,
		"name": pool.Name,
		"url":  pool.URL,
	}).Result()
	if err != nil {
		return err
	}

	// This is a new pool.. add the ID to *:pools
	if newPool {
		servicesKey := fmt.Sprintf("%s:pools", svc.RedisPrefix)
		_, err = svc.Redis.SAdd(servicesKey, pool.ID).Result()
	}
	return err
}
