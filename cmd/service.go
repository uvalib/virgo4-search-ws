package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/uvalib/virgo4-jwt/v4jwt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ServiceContext contains common data used by all handlers
type ServiceContext struct {
	Version        string
	GDB            *gorm.DB
	SuggestorURL   string
	JWTKey         string
	Solr           SolrConfig
	HTTPClient     *http.Client
	FastHTTPClient *http.Client
	SlowHTTPClient *http.Client
	FilterCache    *filterCache
}

// InitializeService will initialize the service context based on the config parameters.
// Any pools found in the DB will be added to the context and polled for status.
// Any errors are FATAL.
func InitializeService(version string, cfg *ServiceConfig) *ServiceContext {
	log.Printf("Initializing Service")
	svc := ServiceContext{Version: version,
		SuggestorURL: cfg.SuggestorURL,
		Solr:         cfg.Solr,
		JWTKey:       cfg.JWTKey}

	log.Printf("Connect to Postgres")
	connStr := fmt.Sprintf("user=%s password=%s dbname=%s host=%s port=%d",
		cfg.DBUser, cfg.DBPass, cfg.DBName, cfg.DBHost, cfg.DBPort)
	gdb, err := gorm.Open(postgres.Open(connStr), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	svc.GDB = gdb

	log.Printf("Create HTTP client for external service calls")
	defaultTransport := &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 600 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 2 * time.Second,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}
	svc.HTTPClient = &http.Client{
		Transport: defaultTransport,
		Timeout:   10 * time.Second,
	}
	svc.FastHTTPClient = &http.Client{
		Transport: defaultTransport,
		Timeout:   5 * time.Second,
	}
	svc.SlowHTTPClient = &http.Client{
		Transport: defaultTransport,
		Timeout:   30 * time.Second,
	}

	log.Printf("Init filter cache")
	svc.FilterCache = newFilterCache(&svc, 300)

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
	type hcResp struct {
		Healthy bool   `json:"healthy"`
		Message string `json:"message,omitempty"`
	}
	hcMap := make(map[string]hcResp)

	var total int64
	dbResp := svc.GDB.Table("sources").Count(&total)
	if dbResp.Error != nil {
		log.Printf("ERROR: Failed response from PSQL healthcheck: %s", dbResp.Error.Error())
		hcMap["postgres"] = hcResp{Healthy: false, Message: dbResp.Error.Error()}
	} else {
		hcMap["postgres"] = hcResp{Healthy: true}
	}

	if svc.SuggestorURL != "" {
		apiURL := fmt.Sprintf("%s/version", svc.SuggestorURL)
		resp, err := svc.FastHTTPClient.Get(apiURL)
		if err != nil {
			log.Printf("ERROR: Suggestor %s ping failed: %s", svc.SuggestorURL, err.Error())
			hcMap["suggestor"] = hcResp{Healthy: false, Message: err.Error()}
		} else {
			hcMap["suggestor"] = hcResp{Healthy: true}
			defer resp.Body.Close()
		}
	}

	c.JSON(http.StatusOK, hcMap)
}

// getBearerToken is a helper to extract the user auth token from the Auth header
func getBearerToken(authorization string) (string, error) {
	components := strings.Split(strings.Join(strings.Fields(authorization), " "), " ")

	// must have two components, the first of which is "Bearer", and the second a non-empty token
	if len(components) != 2 || components[0] != "Bearer" || components[1] == "" {
		return "", fmt.Errorf("invalid Authorization header: [%s]", authorization)
	}

	return components[1], nil
}

// AuthMiddleware is a middleware handler that verifies presence of a
// user JWT in the Authorization header, and verifies its validity
func (svc *ServiceContext) AuthMiddleware(c *gin.Context) {
	tokenStr, err := getBearerToken(c.Request.Header.Get("Authorization"))
	if err != nil {
		log.Printf("Authentication failed: [%s]", err.Error())
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	if tokenStr == "undefined" {
		log.Printf("Authentication failed; bearer token is undefined")
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	log.Printf("Validating JWT auth token...")
	v4Claims, jwtErr := v4jwt.Validate(tokenStr, svc.JWTKey)
	if jwtErr != nil {
		log.Printf("JWT signature for %s is invalid: %s", tokenStr, jwtErr.Error())
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// add the parsed claims and signed JWT string to the request context so other handlers can access it.
	c.Set("jwt", tokenStr)
	c.Set("claims", v4Claims)
	log.Printf("got bearer token: [%s]: %+v", tokenStr, v4Claims)
}

// AdminMiddleware is a middleware handler that verifies that an
// already-authorized user is an admin
func (svc *ServiceContext) AdminMiddleware(c *gin.Context) {
	val, ok := c.Get("claims")

	if ok == false {
		log.Printf("no claims")
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	claims := val.(*v4jwt.V4Claims)

	if claims.Role.String() != "admin" {
		log.Printf("insufficient permissions")
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
}

type timedResponse struct {
	StatusCode int
	Response   []byte
	ElapsedMS  int64
}

func serviceRequest(verb string, url string, body []byte, headers map[string]string, httpClient *http.Client) timedResponse {
	log.Printf("%s %s: %s timeout %.0f", verb, url, body, httpClient.Timeout.Seconds())
	var postReq *http.Request
	if verb == "POST" {
		postReq, _ = http.NewRequest(verb, url, bytes.NewBuffer(body))
	} else {
		postReq, _ = http.NewRequest(verb, url, nil)
	}

	for name, val := range headers {
		postReq.Header.Set(name, val)
	}

	start := time.Now()
	postResp, postErr := httpClient.Do(postReq)
	respBytes, err := handleAPIResponse(url, postResp, postErr)
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	resp := timedResponse{ElapsedMS: elapsedMS}
	if err != nil {
		logLevel := "ERROR"
		// We want to log "not implemented" differently as they are "expected" in some cases
		// (some pools do not support some query types, etc.)
		// This ensures the log filters pick up real errors
		// Also pool timeouts are considered warnings cos we are adding a special filter
		// to track them independently
		if err.StatusCode == http.StatusNotImplemented || err.StatusCode == http.StatusRequestTimeout {
			logLevel = "WARNING"
		}
		log.Printf("%s: Failed response from POST %s - %d:%s. Elapsed Time: %d (ms)",
			logLevel, url, err.StatusCode, err.Message, elapsedMS)
		resp.StatusCode = err.StatusCode
		resp.Response = []byte(err.Message)
	} else {
		log.Printf("Successful response from POST %s. Elapsed Time: %d (ms)", url, elapsedMS)
		resp.StatusCode = postResp.StatusCode
		resp.Response = respBytes
	}

	return resp
}

// RequestError contains http status code and message for a failed service request
type RequestError struct {
	StatusCode int
	Message    string
}

func handleAPIResponse(logURL string, resp *http.Response, err error) ([]byte, *RequestError) {
	if err != nil {
		status := http.StatusBadRequest
		errMsg := err.Error()
		if strings.Contains(err.Error(), "Timeout") {
			status = http.StatusRequestTimeout
			errMsg = fmt.Sprintf("%s timed out", logURL)
		} else if strings.Contains(err.Error(), "connection refused") {
			status = http.StatusServiceUnavailable
			errMsg = fmt.Sprintf("%s refused connection", logURL)
		}
		return nil, &RequestError{StatusCode: status, Message: errMsg}
	} else if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		status := resp.StatusCode
		errMsg := string(bodyBytes)
		return nil, &RequestError{StatusCode: status, Message: errMsg}
	}

	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return bodyBytes, nil
}
