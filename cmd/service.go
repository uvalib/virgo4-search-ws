package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis"
)

// ServiceContext contains common data used by all handlers
type ServiceContext struct {
	Version     string
	RedisPrefix string
	Redis       *redis.Client
	Pools       []*Pool
}

// IsPoolRegistered checks if a pool with the specified URL is registered
func (svc *ServiceContext) IsPoolRegistered(url string) bool {
	if url == "" {
		return false
	}
	for _, pool := range svc.Pools {
		log.Printf("Pool: %s:%t = tgt %s?", pool.URL, pool.Alive, url)
		if pool.URL == url && pool.Alive {
			log.Printf("   match")
			return true
		}
	}
	return false
}

// Init will initialize the service context based on the config parameters. Any
// pools found in redis will be added to the context and polled for status
func (svc *ServiceContext) Init(cfg *ServiceConfig) error {
	log.Printf("Initializing Service...")
	redisHost := fmt.Sprintf("%s:%d", cfg.RedisHost, cfg.RedisPort)
	log.Printf("Connect to redis instance at %s", redisHost)
	svc.RedisPrefix = cfg.RedisPrefix
	redisOpts := redis.Options{
		Addr: redisHost,
		DB:   cfg.RedisDB,
	}
	if cfg.RedisPass != "" {
		redisOpts.Password = cfg.RedisPass
		log.Printf("Connecting to redis DB %d with a password", cfg.RedisDB)
	} else {
		redisOpts.Password = ""
		log.Printf("Connecting to redis DB %d without a password", cfg.RedisDB)
	}
	svc.Redis = redis.NewClient(&redisOpts)

	// See if the connection is good...
	_, err := svc.Redis.Ping().Result()
	if err != nil {
		return err
	}

	// Notes on redis data:
	//   prefix:pools contains a list of IDs for each pool present
	//   prefix:pool:[id] contains a hash with pool details; name and url
	//   prefix:next_pool_id is the next available ID for a new pool
	// Get all of the pools IDs, iterate them to get details and
	// establish connection / status
	poolKeys := fmt.Sprintf("%s:pools", svc.RedisPrefix)
	log.Printf("Redis Connected; reading pools from %s", poolKeys)
	poolIDs := svc.Redis.SMembers(poolKeys).Val()
	for _, poolID := range poolIDs {
		redisID := fmt.Sprintf("%s:pool:%s", svc.RedisPrefix, poolID)
		log.Printf("Get pool %s", redisID)
		pInfo, poolErr := svc.Redis.HGetAll(redisID).Result()
		if poolErr != nil {
			log.Printf("ERROR: Unable to get info for pool %s:%s", redisID, poolErr.Error())
			continue
		}
		log.Printf("Got %+v", pInfo)

		// create a and track a service; assume it is not alive by default
		// ping  will test and update this alive status
		pool := Pool{ID: poolID, URL: pInfo["url"], Alive: false}
		svc.Pools = append(svc.Pools, &pool)
		log.Printf("Init %s...", pool.URL)
		if err := pool.Ping(); err != nil {
			log.Printf("   * %s is not available: %s", pool.URL, err.Error())
		} else {
			log.Printf("   * %s is alive", pool.URL)
			pool.Identify()
		}
	}

	// Start a ticker to periodically poll pools and mark them
	// active or inactive. The weird syntax puts the polling of
	// the ticker channel an a goroutine so it doesn't block
	ticker := time.NewTicker(time.Minute)
	go func() {
		for range ticker.C {
			log.Printf("Pool check heartbeat")
			svc.PingPools()
		}
	}()

	return nil
}

// PingPools checks health of all attached pools and updates their status accordingly
func (svc *ServiceContext) PingPools() {
	errors := false
	for _, p := range svc.Pools {
		if err := p.Ping(); err != nil {
			log.Printf("   * %s offline: %s", p.URL, err.Error())
			errors = true
		}
	}
	if errors == false {
		log.Printf("   * All services online")
	}
}

// IgnoreFavicon is a dummy to handle browser favicon requests without warnings
func (svc *ServiceContext) IgnoreFavicon(c *gin.Context) {
}

// GetVersion reports the version of the serivce
func (svc *ServiceContext) GetVersion(c *gin.Context) {
	build := "unknown"
	// cos our CWD is the bin directory
	files, _ := filepath.Glob("../buildtag.*")
	if len(files) == 1 {
		build = strings.Replace(files[0], "../buildtag.", "", 1)
	}

	vMap := make(map[string]string)
	vMap["version"] = svc.Version
	vMap["build"] = build
	c.JSON(http.StatusOK, vMap)
}

// HealthCheck reports the health of the serivce
func (svc *ServiceContext) HealthCheck(c *gin.Context) {
	type hcResp struct {
		Healthy bool   `json:"healthy"`
		Message string `json:"message,omitempty"`
	}
	hcMap := make(map[string]hcResp)
	for _, p := range svc.Pools {
		if err := p.Ping(); err != nil {
			hcMap[p.URL] = hcResp{Healthy: false, Message: err.Error()}
		} else {
			hcMap[p.URL] = hcResp{Healthy: true}
		}
	}
	if _, err := svc.Redis.Ping().Result(); err != nil {
		hcMap["redis"] = hcResp{Healthy: false, Message: err.Error()}
	} else {
		hcMap["redis"] = hcResp{Healthy: true}
	}
	c.JSON(http.StatusOK, hcMap)
}

func getBearerToken(authorization string) (string, error) {
	components := strings.Split(strings.Join(strings.Fields(authorization), " "), " ")

	// must have two components, the first of which is "Bearer", and the second a non-empty token
	if len(components) != 2 || components[0] != "Bearer" || components[1] == "" {
		return "", fmt.Errorf("Invalid Authorization header: [%s]", authorization)
	}

	return components[1], nil
}

// Authenticate associates a user with an authorized session
// (currently we just just ensure that an Authorization header was sent)
func (svc *ServiceContext) Authenticate(c *gin.Context) {
	token, err := getBearerToken(c.Request.Header.Get("Authorization"))

	if err != nil {
		log.Printf("Authentication failed: [%s]", err.Error())
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// do something with token

	log.Printf("got bearer token: [%s]", token)
}
