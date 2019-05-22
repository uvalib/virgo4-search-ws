package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Pool defines the attributes of a search pool
type Pool struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Alive bool   `json:"alive"`
}

// GetPools gets a list of all active pools and returns it as JSON
func (svc *ServiceContext) GetPools(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not yet implemented")
}

// RegisterPool is called by a pool. It will be added to the list of
// pools that will be queried by  /search
func (svc *ServiceContext) RegisterPool(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not yet implemented")
}
