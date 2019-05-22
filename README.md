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
* POST /api/oIf not present, the POST will fail. ols/register called by seach pools to register as a pool the master will query.
   * JSON Payload: {"name":"NAME", "url":"URL"}. 
   * Note: before add or update, the pool will be pinged and the response scanned for expected content. 
* POST /api/earch search over all pools

### Redis Notes

This repo includes a file containg some initial pools. It is found in `redis_seed.txt`. IMPORTANT: This file has a placeholder `V4_PREFIX` as the redis key for all values. In the command below, sed replaces that placeholder with `prefix`. Be sure to substitute `prefix` with the correct prefix for the installation:

* `cat redis_seed.txt | sed s/V4_PREFIX/prefix/g | redis-cli [-h host] [-p port] -[a auth]`
  
To see what keys are currently available, execute:

* `redis-cli [-h host] [-p port] -[a auth] --scan --pattern prefix:*`

To clean up all of these keys, execute:

* `redis-cli [-h host] [-p port] -[a auth] --scan --pattern prefix:* | xargs redis-cli [-h host] [-p port] -[a auth] del` 
