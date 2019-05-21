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
	Port        int
}

// Load will load the service configuration from env/cmdline
func (cfg *ServiceConfig) Load() {
	log.Printf("Loading configuration...")

	flag.IntVar(&cfg.Port, "port", 8080, "Service port (default 8080)")
	flag.StringVar(&cfg.RedisHost, "redis_host", "localhost", "Redis host (default localhost)")
	flag.IntVar(&cfg.RedisPort, "redis_port", 6379, "Redis port (default 6379)")
	flag.StringVar(&cfg.RedisPass, "redis_pass", "", "Redis password")
	flag.StringVar(&cfg.RedisPrefix, "redis_prefix", "v4_pools", "Redis key prefix")

	flag.Parse()

	log.Printf("%#v", cfg)

	// if anything is still not set, die
	if cfg.RedisHost == "" || cfg.RedisPrefix == "" {
		flag.Usage()
		log.Fatal("FATAL: Missing redis configuration")
	}
}
