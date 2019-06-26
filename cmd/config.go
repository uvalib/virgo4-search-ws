package main

import (
	"flag"
	"log"
)

// ServiceConfig defines all of the archives transfer service configuration paramaters
type ServiceConfig struct {
	RedisHost   string
	RedisPort   int
	RedisPass   string
	RedisPrefix string
	RedisDB     int
	Port        int
	PoolsFile   string
}

// Load will load the service configuration from env/cmdline
func (cfg *ServiceConfig) Load() {
	log.Printf("Loading configuration...")

	flag.IntVar(&cfg.Port, "port", 8080, "Service port (default 8080)")
	flag.StringVar(&cfg.RedisHost, "redis_host", "localhost", "Redis host (default localhost)")
	flag.IntVar(&cfg.RedisPort, "redis_port", 6379, "Redis port (default 6379)")
	flag.StringVar(&cfg.RedisPass, "redis_pass", "", "Redis password")
	flag.StringVar(&cfg.RedisPrefix, "redis_prefix", "v4_pools", "Redis key prefix")
	flag.IntVar(&cfg.RedisDB, "redis_db", 0, "Redis database instance")
	flag.StringVar(&cfg.PoolsFile, "dev_pools", "", "Text file with a list of pools to use in dev env")

	flag.Parse()

	log.Printf("Redis Cfg: %s:%d prefix: %s", cfg.RedisHost, cfg.RedisPort, cfg.RedisPrefix)

	// if anything is still not set, die
	if cfg.RedisHost == "" || cfg.RedisPrefix == "" {
		flag.Usage()
		log.Fatal("FATAL: Missing redis configuration")
	}
}
