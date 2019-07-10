# Virgo4 master search service

This is the core search API for Virgo4. It will fan searches out to known pools
and aggregrate results. The API details are here: https://github.com/uvalib/v4-api

### System Requirements
* GO version 1.12 or greater (mod required)

### Current API

* GET /version : return service version info
* GET /healthcheck : test health of system components; results returned as JSON.
* GET /metrics : returns Prometheus metrics
* GET /api/pools : Get a JSON list search pools that can be queried.
* POST /api/search search over all pools

### Notes

In production, this service depends upon am AWS DynamoDB instance to get 
the authoritative list of pools to be used for searching. 
In development mode, this can be bypassed by passing the command-line param:
`-dev_pools pools.txt`. 
