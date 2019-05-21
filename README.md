# Virgo4 master search service

This is the core search API for Virgo4. It will fan searches out to known pools
and aggregrate results. The API details are here: https://github.com/uvalib/v4-api

### System Requirements
* GO version 1.12 or greater (mod required)

### Current API

* GET /version : return service version info
* GET /healthcheck : test health of system components; results returned as JSON.
* GET /pools : Get a JSON list search pools that can be queried.
* POST /pools : Add a new search pool
   * JSON Payload: {"name":"NAME", "url":"URL"}. 
   * Note: before add or update, the service will be pinged and the response scanned for expected content. If not present, the POST will fail. 
* PUT /pools : Update an existing service
   * Same payload and notes as POST
* POST /search search over all pools

### Redis Notes

This repo includes a file containg some initial pools. It is found in `redis_seed.txt`. IMPORTANT: This file has a placeholder `V4_PREFIX` as the redis key for all values. In the command below, sed replaces that placeholder with `prefix`. Be sure to substitute `prefix` with the correct prefix for the installation:

* `cat redis_seed.txt | sed s/V4_PREFIX/prefix/g | redis-cli [-h host] [-p port] -[a auth]`
  
To see what keys are currently available, execute:

* `redis-cli [-h host] [-p port] -[a auth] --scan --pattern prefix:*`

To clean up all of these keys, execute:

* `redis-cli [-h host] [-p port] -[a auth] --scan --pattern prefix:* | xargs redis-cli [-h host] [-p port] -[a auth] del` 
