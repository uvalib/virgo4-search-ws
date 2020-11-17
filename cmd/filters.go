package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mitchellh/mapstructure"
	"github.com/uvalib/virgo4-api/v4api"
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
		id     string // the global filter id
		label  string // the label shown in client
		source string // the source of data for this pool
		field  string // the Solr field to facet on
		sort   string // "count" or "alpha"
		limit  int    // -1 for unlimited
	}

	// advanced search filter config
	var reqFilters []filterCfg

	reqFilters = append(reqFilters, filterCfg{id: "FilterLibrary", label: "Library", field: "library_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterFormat", label: "Format", field: "format_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterCollection", label: "Collection", field: "source_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterDigitalCollection", label: "Digital Collection", field: "digital_collection_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterCallNumberRange", label: "Call Number Range", field: "call_number_narrow_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterLanguage", label: "Language", field: "language_f", source: "solr", sort: "count", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterGeographicLocation", label: "Geographic Location", field: "region_f", source: "solr", sort: "count", limit: 500})
	reqFilters = append(reqFilters, filterCfg{id: "FilterPermissions", label: "Permissions", field: "use_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterLicense", label: "License", field: "license_class_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterFundCode", label: "Fund Code", field: "fund_code_f", source: "solr", sort: "index", limit: -1})
	reqFilters = append(reqFilters, filterCfg{id: "FilterShelfLocation", label: "Shelf Location", field: "location2_f", source: "solr", sort: "index", limit: -1})

	// create Solr request
	var req solrRequest

	req.Params = solrRequestParams{Q: "*:*", Rows: 0, Fq: []string{"+shadowed_location_f:VISIBLE"}}

	req.Facets = make(map[string]solrRequestFacet)
	for _, config := range reqFilters {
		req.Facets[config.id] = solrRequestFacet{Type: "terms", Field: config.field, Sort: config.sort, Limit: config.limit}
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
			Val   string `json:"val"`
			Count int    `json:"count"`
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

	// build response array

	filters := []v4api.QueryFilter{}

	for _, config := range reqFilters {
		var facet solrResponseFacet

		cfg := &mapstructure.DecoderConfig{Metadata: nil, Result: &facet, TagName: "json", ZeroFields: true}
		dec, _ := mapstructure.NewDecoder(cfg)

		if mapDecErr := dec.Decode(solrResp.Facets[config.id]); mapDecErr != nil {
			// probably want to error handle, but for now, just drop this field
			continue
		}

		sources := []string{config.source}
		filterValues := []v4api.QueryFilterValue{}

		for _, bucket := range facet.Buckets {
			filterValue := v4api.QueryFilterValue{Value: bucket.Val, Count: bucket.Count}
			filterValues = append(filterValues, filterValue)
		}

		filters = append(filters, v4api.QueryFilter{ID: config.id, Label: config.label, Sources: sources, Values: filterValues})
	}

	c.JSON(http.StatusOK, filters)
}
