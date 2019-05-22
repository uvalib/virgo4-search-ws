package main

import (
	"fmt"
	"log"

	"github.com/gin-gonic/gin"
)

// Version of the service
const version = "1.0.0"

/**
 * MAIN
 */
func main() {
	log.Printf("===> V4 Master Search service staring up <===")

	// Get config params; service port, directories, DB
	cfg := ServiceConfig{}
	cfg.Load()
	svc := ServiceContext{Version: version}
	err := svc.Init(&cfg)
	if err != nil {
		log.Fatalf("Unable to initialize service: %s", err.Error())
	}

	log.Printf("Setup routes...")
	gin.SetMode(gin.ReleaseMode)
	gin.DisableConsoleColor()
	router := gin.Default()
	router.GET("/version", svc.GetVersion)
	router.GET("/healthcheck", svc.HealthCheck)
	router.GET("/pools", svc.GetPools)
	router.POST("/pools/register", svc.RegisterPool)
	router.POST("/search", svc.Search)

	portStr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Start service v%s on port %s", version, portStr)
	log.Fatal(router.Run(portStr))
}
