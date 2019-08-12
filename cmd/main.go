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

	// Get config params and use them to init service context. Any issues are fatal
	cfg := LoadConfiguration()
	svc := InitializeService(version, cfg)

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

	router.GET("/", svc.GetVersion)
	router.GET("/favicon.ico", svc.IgnoreFavicon)
	router.GET("/version", svc.GetVersion)
	router.GET("/healthcheck", svc.HealthCheck)
	api := router.Group("/api")
	{
		api.GET("/pools", svc.AuthMiddleware, svc.GetPoolsRequest)
		api.POST("/search", svc.AuthMiddleware, svc.Search)
	}

	portStr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Start service v%s on port %s", version, portStr)
	log.Fatal(router.Run(portStr))
}
