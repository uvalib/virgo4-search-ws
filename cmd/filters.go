package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/mitchellh/mapstructure"

	"github.com/gin-gonic/gin"
)

type solrRequestParams struct {
	Rows int      `json:"rows"`
	Fq   []string `json:"fq,omitempty"`
	Q    string   `json:"q,omitempty"`
}

type solrRequestFacet struct {
	Type  string `json:"type,omitempty"`
	Field string `json:"field,omitempty"`
	Sort  string `json:"sort,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type solrRequest struct {
	Params solrRequestParams           `json:"params"`
	Facets map[string]solrRequestFacet `json:"facet,omitempty"`
}

// GetSearchFilters will return all available advanced search filters
func (svc *ServiceContext) GetSearchFilters(c *gin.Context) {
	log.Printf("Get advanced search filters")

	type filterCfg struct {
		field string // the Solr field to facet on
		sort  string // "count" or "alpha"
		limit int    // -1 for unlimited
	}

	// advanced search filter config
	reqFilters := make(map[string]filterCfg)

	reqFilters["Collection"] = filterCfg{field: "source_f", sort: "count", limit: -1}
	//reqFilters["Series"] = filterCfg{field: "title_series_f", sort: "count", limit: 500}

	// create Solr request
	var req solrRequest

	req.Params = solrRequestParams{Q: "*:*", Rows: 0, Fq: []string{"+shadowed_location_f:VISIBLE"}}

	req.Facets = make(map[string]solrRequestFacet)
	for label, config := range reqFilters {
		req.Facets[label] = solrRequestFacet{Type: "terms", Field: config.field, Sort: config.sort, Limit: config.limit}
	}

	// send the request
	respBytes, err := svc.solrPost("select", req)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Message)
		return
	}

	// the structure of a Solr response facet
	type solrResponseFacet struct {
		Buckets []struct {
			Val string `json:"val"`
		} `json:"buckets,omitempty"`
	}

	// the Facets field will contain the facets we want, plus additional non-facet field(s).
	// we will parse the map for the facet labels we requested, as they will be the response labels.
	var solrResp struct {
		Facets map[string]interface{} `json:"facets"`
	}

	if err := json.Unmarshal(respBytes, &solrResp); err != nil {
		log.Printf("ERROR: Unable to parse Solr response: %s", err.Error())
		c.String(http.StatusInternalServerError, err.Error())
		return
	}

	// build response
	type filter struct {
		Label  string   `json:"label"`
		Field  string   `json:"field"`
		Values []string `json:"values"`
	}

	out := make([]filter, 0)

	for label, config := range reqFilters {
		var facet solrResponseFacet

		cfg := &mapstructure.DecoderConfig{Metadata: nil, Result: &facet, TagName: "json", ZeroFields: true}
		dec, _ := mapstructure.NewDecoder(cfg)

		if mapDecErr := dec.Decode(solrResp.Facets[label]); mapDecErr != nil {
			// probably want to error handle, but for now, just drop this field
			continue
		}

		var buckets []string
		for _, bucket := range facet.Buckets {
			buckets = append(buckets, bucket.Val)
		}

		out = append(out, filter{Label: label, Field: config.field, Values: buckets})
	}

	c.JSON(http.StatusOK, out)
}
