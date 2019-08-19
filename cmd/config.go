package main

import (
	"flag"
	"log"
)

// ServiceConfig defines all of the archives transfer service configuration paramaters
type ServiceConfig struct {
	DBHost string
	DBPort int
	DBName string
	DBUser string
	DBPass string
	Port   int
}

// LoadConfiguration will load the service configuration from env/cmdline
// and return a pointer to it. Any failures are fatal.
func LoadConfiguration() *ServiceConfig {
	log.Printf("Loading configuration...")
	var cfg ServiceConfig
	flag.IntVar(&cfg.Port, "port", 8080, "Service port (default 8080)")
	flag.StringVar(&cfg.DBHost, "dbhost", "localhost", "Database host")
	flag.IntVar(&cfg.DBPort, "dbport", 5432, "Database port")
	flag.StringVar(&cfg.DBName, "dbname", "virgo4", "Database name")
	flag.StringVar(&cfg.DBUser, "dbuser", "v4user", "Database user")
	flag.StringVar(&cfg.DBPass, "dbpass", "pass", "Database password")

	flag.Parse()

	return &cfg
}
