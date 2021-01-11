package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/uvalib/virgo4-api/v4api"
	"github.com/uvalib/virgo4-jwt/v4jwt"
)

type filterResponse struct {
	pool    *pool
	filters *v4api.PoolFacets
}

type filterCache struct {
	svc             *ServiceContext
	refreshInterval int
	currentFilters  []v4api.QueryFilter
}

func newFilterCache(svc *ServiceContext, interval int) *filterCache {
	cache := filterCache{
		svc:             svc,
		refreshInterval: interval,
		currentFilters:  []v4api.QueryFilter{},
	}

	go cache.monitorFilters()

	return &cache
}

func (f *filterCache) monitorFilters() {
	for {
		f.refreshCache()
		log.Printf("[FILTERS] refresh scheduled in %d seconds", f.refreshInterval)
		time.Sleep(time.Duration(f.refreshInterval) * time.Second)
	}
}

func (f *filterCache) refreshCache() {
	log.Printf("[FILTERS] refreshing filters...")

	acceptLang := "en-US"

	pools, err := f.svc.lookupPools(acceptLang)
	if err != nil {
		log.Printf("[FILTERS] ERROR: Unable to get default pools: %+v", err)
		return
	}

	// query up to one pool of each source that supports filters

	// NOTE: the order of this list dictates the order of preference for
	// attributes of the combined filters, including filter label and sort order.
	// this is important since a) the solr pools have more translations for shared
	// filter IDs, and b) only the solr pools currently specify bucket sort order.
	sources := []string{"solr", "solr-images", "eds"}

	filterResps := make(map[string]*filterResponse)

	channel := make(chan *filterResponse)
	outstandingRequests := 0

	for _, source := range sources {
		for _, pool := range pools {
			if pool.V4ID.Source == source {
				log.Printf("[FILTERS] source [%s] will query pool [%s]", source, pool.V4ID.ID)
				outstandingRequests++
				go f.getPoolFilters(pool, acceptLang, channel, f.svc.SlowHTTPClient)
				break
			}
		}
	}

	for outstandingRequests > 0 {
		filterResp := <-channel
		if filterResp.filters != nil {
			filterResps[filterResp.pool.V4ID.Source] = filterResp
		}
		outstandingRequests--
	}

	// sanity check: only update if we received as many responses as there are sources
	if len(filterResps) != len(sources) {
		log.Printf("[FILTERS] not all sources returned filters; skipping refresh")
		return
	}

	// merge filter lists from each representative pool

	type singleFilter struct {
		source string
		filter v4api.Facet
	}

	filterMap := make(map[string][]singleFilter)
	filterOrder := []string{}

	// collect source/filter list for each filter ID
	for _, source := range sources {
		filterResp := filterResps[source]
		if filterResp == nil {
			continue
		}

		for _, facet := range filterResp.filters.FacetList {
			log.Printf("[FILTERS] source [%s] provided filter: [%s] (%d values)",
				filterResp.pool.V4ID.Source, facet.ID, len(facet.Buckets))

			single := singleFilter{
				source: filterResp.pool.V4ID.Source,
				filter: facet,
			}

			if len(filterMap[facet.ID]) == 0 {
				filterOrder = append(filterOrder, facet.ID)
			}

			filterMap[facet.ID] = append(filterMap[facet.ID], single)
		}
	}

	combined := []v4api.QueryFilter{}

	// combine filter lists for each filter ID
	for _, filterID := range filterOrder {
		filterList := filterMap[filterID]

		queryFilter := v4api.QueryFilter{ID: filterID}
		bucketSort := ""

		valuesMap := make(map[string]int)

		for _, filter := range filterList {
			queryFilter.Sources = append(queryFilter.Sources, filter.source)

			// first source to label this filter wins
			if queryFilter.Label == "" && filter.filter.Name != "" {
				queryFilter.Label = filter.filter.Name
			}

			// first source to specify sort order wins
			if bucketSort == "" && filter.filter.Sort != "" {
				bucketSort = filter.filter.Sort
			}

			// hide if any source marks this as hidden
			queryFilter.Hidden = queryFilter.Hidden || filter.filter.Hidden

			// accumulate counts for specific values
			for _, bucket := range filter.filter.Buckets {
				valuesMap[bucket.Value] += bucket.Count
			}
		}

		// build and sort bucket list
		var buckets []v4api.QueryFilterValue

		for value, count := range valuesMap {
			filterValue := v4api.QueryFilterValue{Value: value, Count: count}
			buckets = append(buckets, filterValue)
		}

		switch bucketSort {
		case "alpha":
			sort.Slice(buckets, func(i, j int) bool {
				// bucket values are unique so this is the only test we need
				return buckets[i].Value < buckets[j].Value
			})

		default:
			// sort by count
			sort.Slice(buckets, func(i, j int) bool {
				if buckets[i].Count > buckets[j].Count {
					return true
				}

				if buckets[i].Count < buckets[j].Count {
					return false
				}

				// items with the same count get sorted alphabetically for consistency
				return buckets[i].Value < buckets[j].Value
			})
		}

		queryFilter.Values = buckets

		log.Printf("[FILTERS] created merged filter: [%s] (%d values)", queryFilter.ID, len(queryFilter.Values))

		combined = append(combined, queryFilter)
	}

	f.currentFilters = combined
}

func (f *filterCache) getFilters() []v4api.QueryFilter {
	return f.currentFilters
}

// GetSearchFilters will return all available advanced search filters
func (svc *ServiceContext) GetSearchFilters(c *gin.Context) {
	log.Printf("Get advanced search filters")
	c.JSON(http.StatusOK, svc.FilterCache.getFilters())
}

// Goroutine to do a pool pre-search filter lookup and return the results over a channel
func (f *filterCache) getPoolFilters(pool *pool, language string, channel chan *filterResponse, httpClient *http.Client) {
	var method string
	var endpoint string
	var v4query []byte
	var includeFilters map[string]bool

	chanResp := &filterResponse{pool: pool}

	claims := v4jwt.V4Claims{IsUVA: true}

	token, jwtErr := v4jwt.Mint(claims, 5*time.Minute, f.svc.JWTKey)
	if jwtErr != nil {
		log.Printf("[FILTERS] ERROR: failed to mint JWT: %s", jwtErr.Error())
		channel <- chanResp
		return
	}

	headers := map[string]string{
		"Accept-Language": language,
		"Authorization":   fmt.Sprintf("Bearer %s", token),
	}

	switch {
	case pool.V4ID.Source == "eds":
		method = "POST"
		endpoint = "api/search/facets"
		v4query = []byte(`{"query":"keyword:{*}"}`)
		headers["Content-Type"] = "application/json"
		includeFilters = map[string]bool{
			"ContentProvider":   true,
			"SubjectGeographic": true,
			"Language":          true,
			"Publisher":         true,
			"SourceType":        true,
		}

	case strings.HasPrefix(pool.V4ID.Source, "solr") == true:
		method = "GET"
		endpoint = "api/filters"

	default:
		log.Printf("[FILTERS] ERROR: unhandled pool source: [%s]", pool.V4ID.Source)
		channel <- chanResp
		return
	}

	url := fmt.Sprintf("%s/%s", pool.PrivateURL, endpoint)

	resp := serviceRequest(method, url, v4query, headers, httpClient)
	if resp.StatusCode != http.StatusOK {
		log.Printf("[FILTERS] ERROR: %s pool: http status code: %d", pool.V4ID.Source, resp.StatusCode)
		channel <- chanResp
		return
	}

	var filters v4api.PoolFacets
	err := json.Unmarshal(resp.Response, &filters)
	if err != nil {
		log.Printf("[FILTERS] ERROR: %s pool: malformed response: %s", pool.V4ID.Source, err.Error())
		channel <- chanResp
		return
	}

	// ensure there are actually filters (the pools might send empty lists on error)
	if len(filters.FacetList) == 0 {
		log.Printf("[FILTERS] ERROR: %s pool: response contains no filters", pool.V4ID.Source)
		channel <- chanResp
		return
	}

	// if defined, only include specific filters
	if len(includeFilters) > 0 {
		var facets []v4api.Facet

		for _, facet := range filters.FacetList {
			if _, ok := includeFilters[facet.ID]; ok == true {
				facets = append(facets, facet)
			}
		}

		filters.FacetList = facets
	}

	// ensure filters are named appropriately
	for i := range filters.FacetList {
		facet := &filters.FacetList[i]

		if strings.HasPrefix(facet.ID, "Filter") == false {
			facet.ID = "Filter" + facet.ID
		}
	}

	chanResp.filters = &filters

	channel <- chanResp
}
