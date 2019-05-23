package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Sort cantains data to define search sort order
type Sort struct {
	Field string `json:"field"`
	Order string `json:"order"`
}

// Query defines the search terms and order
type Query struct {
	Keyword string `json:"keyword"`
	Author  string `json:"author"`
	Title   string `json:"title"`
	Subject string `json:"subject"`
	Sort    Sort   `json:"sort"`
}

// Pagination cantains pagination info
type Pagination struct {
	Start int `json:"start"`
	Rows  int `json:"rows"`
}

// Preferences cantains search preferences
type Preferences struct {
	DefaultPool  string   `json:"default_search_pool"`
	ExcludePools []string `json:"excluded_pools"`
}

// Search contains all of the data necessary for a client seatch request
type Search struct {
	Query       Query       `json:"query"`
	Pagination  Pagination  `json:"pagination"`
	Preferences Preferences `json:"search_preferences"`
}

// Search queries all pools for results, collects and curates results. Responds with JSON.
func (svc *ServiceContext) Search(c *gin.Context) {
	var req Search
	if err := c.BindJSON(&req); err != nil {
		log.Printf("ERROR: unablt to parse search request: %s", err.Error())
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("GOT %+v", req)
	c.String(http.StatusNotImplemented, "Not yet implemented")
}
