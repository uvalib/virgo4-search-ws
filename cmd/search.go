package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Search queries all pools for results, collects and curates results. Responds with JSON.
func (svc *ServiceContext) Search(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Not yet implemented")
}
