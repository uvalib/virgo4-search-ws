package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ServiceContext contains common data used by all handlers
type ServiceContext struct {
	Version string
}

// Init will initialize the service context based on the config parameters
func (svc *ServiceContext) Init(cfg *ServiceConfig) {
	log.Printf("Initializing Service...")
}

// GetVersion reports the version of the serivce
func (svc *ServiceContext) GetVersion(c *gin.Context) {
	c.String(http.StatusOK, "V4 Master Search Service version %s", svc.GetVersion)
}

// HealthCheck reports the health of the serivce
func (svc *ServiceContext) HealthCheck(c *gin.Context) {
	c.String(http.StatusOK, "alive")
}
