package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// ServiceContext contains common data used by all handlers
type ServiceContext struct {
	Version      string
	PoolsTable   string
	DevPoolsFile string
	DynamoDB     *dynamodb.DynamoDB
	Pools        []*Pool
	I18NBundle   *i18n.Bundle
}

// InitializeService will initialize the service context based on the config parameters.
// Any pools found in the DB will be added to the context and polled for status.
// Any errors are FATAL.
func InitializeService(version string, cfg *ServiceConfig) *ServiceContext {
	log.Printf("Initializing Service")
	svc := ServiceContext{Version: version, Pools: make([]*Pool, 0)}

	log.Printf("Init localization")
	svc.I18NBundle = i18n.NewBundle(language.English)
	svc.I18NBundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	svc.I18NBundle.MustLoadMessageFile("./i18n/active.en.toml")
	svc.I18NBundle.MustLoadMessageFile("./i18n/active.es.toml")

	if cfg.PoolsFile != "" {
		svc.DevPoolsFile = cfg.PoolsFile
		svc.LoadDevPools()
	} else {
		if cfg.AWSAccessKey == "" {
			log.Printf("Init AWS DynamoDB Session using AWS role")
			sess := session.Must(session.NewSession(&aws.Config{
				Region: aws.String(cfg.AWSRegion),
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
			log.Fatalf("Unable to initialize search pools: %s", err.Error())
		}
	}

	// Start a ticker to periodically poll pools and mark them
	// active or inactive. The weird syntax puts the polling of
	// the ticker channel an a goroutine so it doesn't block
	log.Printf("Start pool hearbeat ticker")
	ticker := time.NewTicker(time.Minute)
	go func() {
		for range ticker.C {
			log.Printf("Pool check heartbeat; checking %d pools.", len(svc.Pools))
			for _, p := range svc.Pools {
				if err := p.Ping(); err != nil {
					log.Printf("   * %s failed ping: %s", p.PrivateURL, err.Error())
				}
			}
		}
	}()

	return &svc
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
	if len(svc.Pools) == 0 {
		c.String(http.StatusInternalServerError, "No pools registered")
		return
	}
	type hcResp struct {
		Healthy bool   `json:"healthy"`
		Message string `json:"message,omitempty"`
	}
	hcMap := make(map[string]hcResp)
	healthyCount := 0
	for _, p := range svc.Pools {
		if err := p.Ping(); err != nil {
			hcMap[p.PrivateURL] = hcResp{Healthy: false, Message: err.Error()}
		} else {
			hcMap[p.PrivateURL] = hcResp{Healthy: true}
			healthyCount++
		}
	}
	if healthyCount == 0 {
		c.String(http.StatusInternalServerError, fmt.Sprintf("%d pools registered, all report errors.", len(svc.Pools)))
	} else {
		c.JSON(http.StatusOK, hcMap)
	}
}

// getBearerToken is a helper to extract the user auth token from the Auth header
func getBearerToken(authorization string) (string, error) {
	components := strings.Split(strings.Join(strings.Fields(authorization), " "), " ")

	// must have two components, the first of which is "Bearer", and the second a non-empty token
	if len(components) != 2 || components[0] != "Bearer" || components[1] == "" {
		return "", fmt.Errorf("Invalid Authorization header: [%s]", authorization)
	}

	return components[1], nil
}

// AuthMiddleware is a middleware handler that verifies presence of a
// user Bearer token in the Authorization header.
func (svc *ServiceContext) AuthMiddleware(c *gin.Context) {
	token, err := getBearerToken(c.Request.Header.Get("Authorization"))

	if err != nil {
		log.Printf("Authentication failed: [%s]", err.Error())
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// do something with token

	log.Printf("got bearer token: [%s]", token)
}
