package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gin-gonic/gin"
	dbx "github.com/go-ozzo/ozzo-dbx"
	_ "github.com/lib/pq"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/uvalib/virgo4-jwt/v4jwt"
	"golang.org/x/text/language"
)

// ServiceContext contains common data used by all handlers
type ServiceContext struct {
	Version        string
	DB             *dbx.DB
	SuggestorURL   string
	JWTKey         string
	I18NBundle     *i18n.Bundle
	HTTPClient     *http.Client
	FastHTTPClient *http.Client
}

// InitializeService will initialize the service context based on the config parameters.
// Any pools found in the DB will be added to the context and polled for status.
// Any errors are FATAL.
func InitializeService(version string, cfg *ServiceConfig) *ServiceContext {
	log.Printf("Initializing Service")
	svc := ServiceContext{Version: version, SuggestorURL: cfg.SuggestorURL, JWTKey: cfg.JWTKey}

	log.Printf("Connect to Postgres")
	connStr := fmt.Sprintf("user=%s password=%s dbname=%s host=%s port=%d sslmode=disable",
		cfg.DBUser, cfg.DBPass, cfg.DBName, cfg.DBHost, cfg.DBPort)
	db, err := dbx.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	db.LogFunc = log.Printf
	svc.DB = db

	log.Printf("Init localization")
	svc.I18NBundle = i18n.NewBundle(language.English)
	svc.I18NBundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	svc.I18NBundle.MustLoadMessageFile("./i18n/active.en.toml")
	svc.I18NBundle.MustLoadMessageFile("./i18n/active.es.toml")

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

	tq := svc.DB.NewQuery("select count(*) as total from sources")
	var total int
	err := tq.Row(&total)
	if err != nil {
		log.Printf("ERROR: Failed response from PSQL healthcheck: %s", err.Error())
		hcMap["postgres"] = hcResp{Healthy: false, Message: err.Error()}
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

type timedResponse struct {
	StatusCode      int
	ContentLanguage string
	Response        []byte
	ElapsedMS       int64
}

func servicePost(url string, body []byte, headers map[string]string, httpClient *http.Client) timedResponse {
	log.Printf("POST %s: %s timeout %.0f", url, body, httpClient.Timeout.Seconds())
	postReq, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	for name, val := range headers {
		postReq.Header.Set(name, val)
	}

	start := time.Now()
	postResp, err := httpClient.Do(postReq)
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	resp := timedResponse{ElapsedMS: elapsedMS}
	if err != nil {
		resp.Response = []byte(err.Error())
		resp.StatusCode = http.StatusInternalServerError
		if strings.Contains(err.Error(), "Timeout") {
			resp.StatusCode = http.StatusRequestTimeout
			resp.Response = []byte(fmt.Sprintf("POST %s search timed out", url))
		} else if strings.Contains(err.Error(), "connection refused") {
			resp.StatusCode = http.StatusServiceUnavailable
			resp.Response = []byte(fmt.Sprintf("%s is offline", url))
		}
		log.Printf("ERROR: Failed response from POST %s - %d:%s. Elapsed Time: %d (ms)",
			url, resp.StatusCode, resp.Response, elapsedMS)
	} else {
		defer postResp.Body.Close()
		bodyBytes, _ := ioutil.ReadAll(postResp.Body)
		resp.StatusCode = postResp.StatusCode
		resp.Response = bodyBytes
		if resp.StatusCode != http.StatusOK {
			log.Printf("ERROR: Failed response from POST %s - %d:%s. Elapsed Time: %d (ms)",
				url, postResp.StatusCode, resp.Response, elapsedMS)
		} else {
			log.Printf("Successful response from POST %s. Elapsed Time: %d (ms)", url, elapsedMS)
			resp.ContentLanguage = postResp.Header.Get("Content-Language")
			if resp.ContentLanguage == "" {
				resp.ContentLanguage = postReq.Header.Get("Accept-Language")
			}
			if resp.ContentLanguage == "" {
				resp.ContentLanguage = "en-US"
			}
		}
	}

	return resp
}
