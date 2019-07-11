package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
)

// ServiceContext contains common data used by all handlers
type ServiceContext struct {
	Version    string
	PoolsTable string
	DynamoDB   *dynamodb.DynamoDB
	Pools      []*Pool
}

// IsPoolRegistered checks if a pool with the specified URL is registered
// NOTE: This will check both public and private URLs to be sure
func (svc *ServiceContext) IsPoolRegistered(url string) bool {
	if url == "" {
		return false
	}
	for _, pool := range svc.Pools {
		if (pool.PrivateURL == url || pool.PublicURL == url) && pool.Alive {
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
	if cfg.PoolsFile != "" {
		svc.LoadDevPools(cfg.PoolsFile)
	} else {
		if cfg.AWSAccessKey == "" {
			log.Printf("Init AWS DynamoDB Session using AWS role")
			sess := session.Must(session.NewSession(&aws.Config{
				Region:      aws.String(cfg.AWSRegion),
				Credentials: credentials.NewStaticCredentials(cfg.AWSAccessKey, cfg.AWSSecretKey, ""),
			}))
			svc.DynamoDB = dynamodb.New(sess)
		} else {
			log.Printf("Init AWS DynamoDB Session using passed keys")
			sess := session.Must(session.NewSession(&aws.Config{
				Region:      aws.String(cfg.AWSRegion),
				Credentials: credentials.NewStaticCredentials(cfg.AWSAccessKey, cfg.AWSSecretKey, ""),
			}))
			svc.DynamoDB = dynamodb.New(sess)
		}
		svc.PoolsTable = cfg.DynamoDBTable
		err := svc.UpdateAuthoritativePools()
		if err != nil {
			return err
		}
	}

	// Start a ticker to periodically poll pools and mark them
	// active or inactive. The weird syntax puts the polling of
	// the ticker channel an a goroutine so it doesn't block
	log.Printf("Start pool hearbeat ticker")
	ticker := time.NewTicker(time.Minute)
	go func() {
		for range ticker.C {
			log.Printf("Pool check heartbeat")
			svc.PingPools()
		}
	}()

	return nil
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
			hcMap[p.PrivateURL] = hcResp{Healthy: false, Message: err.Error()}
		} else {
			hcMap[p.PrivateURL] = hcResp{Healthy: true}
		}
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
// (currently we just just ensure that a bearer token was sent)
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
