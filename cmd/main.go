package main

import (
	"fmt"
	"log"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	ginprometheus "github.com/zsais/go-gin-prometheus"
)

// Version of the service
const version = "1.0.0"

/**
 * MAIN
 */
func main() {
	log.Printf("===> V4 search service staring up <===")

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
	corsCfg := cors.DefaultConfig()
	corsCfg.AllowAllOrigins = true
	corsCfg.AllowCredentials = true
	corsCfg.AddAllowHeaders("Authorization")
	router.Use(cors.New(corsCfg))
	p := ginprometheus.NewPrometheus("gin")
	p.Use(router)

	router.GET("/", svc.Authenticate, svc.GetVersion)
	router.GET("/favicon.ico", svc.Authenticate, svc.IgnoreFavicon)
	router.GET("/version", svc.Authenticate, svc.GetVersion)
	router.GET("/healthcheck", svc.Authenticate, svc.HealthCheck)
	api := router.Group("/api")
	{
		api.GET("/pools", svc.Authenticate, svc.GetPools)
		api.POST("/pools/register", svc.Authenticate, svc.RegisterPool)
		api.DELETE("/pools/register", svc.Authenticate, svc.DeRegisterPool)
		api.POST("/search", svc.Authenticate, svc.Search)
	}

	portStr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Start service v%s on port %s", version, portStr)
	log.Fatal(router.Run(portStr))
}
