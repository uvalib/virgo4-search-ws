package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/uvalib/virgo4-api/v4api"
)

type filterResponse struct {
	pool    *pool
	filters *v4api.PoolFacets
}

// GetSearchFilters will return all available advanced search filters
func (svc *ServiceContext) GetSearchFilters(c *gin.Context) {
	log.Printf("Get advanced search filters")

	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		acceptLang = "en-US"
	}
	localizer := i18n.NewLocalizer(svc.I18NBundle, acceptLang)

	// Pools have already been placed in request context by poolsMiddleware. Get them or fail
	pools := getPoolsFromContext(c)
	if len(pools) == 0 {
		err := searchError{Message: localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "NoPools"}),
			Details: ""}
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	// query up to one pool of each source that supports filters

	// NOTE: the order of this list dictates the order of preference for
	// attributes of the combined filters, including filter label and sort order.
	// this is important since a) the solr pools have more translations for shared
	// filter IDs, and b) only the solr pools currently specify bucket sort order.
	sources := []string{"solr", "eds"}

	filterResps := make(map[string]*filterResponse)

	channel := make(chan *filterResponse)
	outstandingRequests := 0

	for _, source := range sources {
		for _, pool := range pools {
			if pool.V4ID.Source == source {
				log.Printf("source [%s] => pool [%s]", source, pool.V4ID.ID)
				outstandingRequests++
				go getPoolFilters(c, pool, acceptLang, channel, svc.SlowHTTPClient)
				break
			}
		}
	}

	for outstandingRequests > 0 {
		filterResp := <-channel
		if filterResp != nil {
			filterResps[filterResp.pool.V4ID.Source] = filterResp
		}
		outstandingRequests--
	}

	// merge filter lists from each representative pool

	type singleFilter struct {
		source string
		filter v4api.Facet
	}

	filterMap := make(map[string][]singleFilter)

	// collect source/filter list for each filter ID
	for _, source := range sources {
		filterResp := filterResps[source]
		if filterResp == nil {
			continue
		}

		for _, facet := range filterResp.filters.FacetList {
			single := singleFilter{
				source: filterResp.pool.V4ID.Source,
				filter: facet,
			}

			filterMap[facet.ID] = append(filterMap[facet.ID], single)
		}
	}

	combined := []v4api.QueryFilter{}

	// combine filter lists for each filter ID
	for filterID, filterList := range filterMap {
		queryFilter := v4api.QueryFilter{ID: filterID}
		bucketSort := ""

		sourcesMap := make(map[string]bool)
		valuesMap := make(map[string]int)

		for _, filter := range filterList {
			sourcesMap[filter.source] = true

			// first source to label this filter wins
			if queryFilter.Label == "" && filter.filter.Name != "" {
				queryFilter.Label = filter.filter.Name
			}

			// first source to specify sort order wins
			if bucketSort == "" && filter.filter.Sort != "" {
				bucketSort = filter.filter.Sort
			}

			// accumulate counts for specific values
			for _, bucket := range filter.filter.Buckets {
				valuesMap[bucket.Value] += bucket.Count
			}
		}

		// add sources that contributed to this filter
		for source := range sourcesMap {
			queryFilter.Sources = append(queryFilter.Sources, source)
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

		combined = append(combined, queryFilter)
	}

	c.JSON(http.StatusOK, combined)
}

// Goroutine to do a pool pre-search filter lookup and return the results over a channel
func getPoolFilters(c *gin.Context, pool *pool, language string, channel chan *filterResponse, httpClient *http.Client) {
	var method string
	var endpoint string
	var v4query []byte
	var includeFilters map[string]bool

	headers := map[string]string{
		"Accept-Language": language,
		"Authorization":   c.GetHeader("Authorization"),
	}

	chanResp := &filterResponse{pool: pool}

	switch pool.V4ID.Source {
	case "eds":
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

	case "solr":
		method = "GET"
		endpoint = "api/filters"

	default:
		channel <- chanResp
	}

	url := fmt.Sprintf("%s/%s", pool.PrivateURL, endpoint)

	resp := serviceRequest(method, url, v4query, headers, httpClient)
	if resp.StatusCode != http.StatusOK {
		channel <- chanResp
		return
	}

	var filters v4api.PoolFacets
	err := json.Unmarshal(resp.Response, &filters)
	if err != nil {
		log.Printf("ERROR: Malformed filters response: %s", err.Error())
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
