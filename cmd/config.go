package main

import (
	"flag"
	"log"
)

// SolrConfig wraps up the config for solr acess
type SolrConfig struct {
	URL  string
	Core string
}

// ServiceConfig defines all of the archives transfer service configuration paramaters
type ServiceConfig struct {
	SuggestorURL string
	DBHost       string
	DBPort       int
	DBName       string
	DBUser       string
	DBPass       string
	Port         int
	JWTKey       string
	Solr         SolrConfig
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
	flag.StringVar(&cfg.SuggestorURL, "suggestor", "", "Suggestor service URL")
	flag.StringVar(&cfg.JWTKey, "jwtkey", "", "JWT signature key")

	// Solr config
	flag.StringVar(&cfg.Solr.URL, "solr", "", "Solr URL for journal browse")
	flag.StringVar(&cfg.Solr.Core, "core", "test_core", "Solr core for journal browse")

	flag.Parse()

	if cfg.SuggestorURL == "" {
		log.Fatal("suggestor param is required")
	} else {
		log.Printf("Suggestor API endpoint: %s", cfg.SuggestorURL)
	}
	if cfg.JWTKey == "" {
		log.Fatal("jwtkey param is required")
	}
	if cfg.Solr.URL == "" || cfg.Solr.Core == "" {
		log.Fatal("solr and core params are required")
	} else {
		log.Printf("Solr endpoint: %s/%s", cfg.Solr.URL, cfg.Solr.Core)
	}

	return &cfg
}
