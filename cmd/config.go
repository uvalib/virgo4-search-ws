package main

import (
	"flag"
	"log"
)

// ServiceConfig defines all of the archives transfer service configuration paramaters
type ServiceConfig struct {
	AWSAccessKey  string
	AWSSecretKey  string
	AWSRegion     string
	DynamoDBTable string
	Port          int
	PoolsFile     string
}

// Load will load the service configuration from env/cmdline
func (cfg *ServiceConfig) Load() {
	log.Printf("Loading configuration...")

	flag.IntVar(&cfg.Port, "port", 8080, "Service port (default 8080)")
	flag.StringVar(&cfg.AWSAccessKey, "aws_access", "", "AWS Access Key")
	flag.StringVar(&cfg.AWSSecretKey, "aws_secret", "", "AWS Secret Key")
	flag.StringVar(&cfg.AWSRegion, "aws_region", "us-east-1", "AWS region")
	flag.StringVar(&cfg.DynamoDBTable, "ddb_table", "V4SearchPools", "DynamoDB table name")
	flag.StringVar(&cfg.PoolsFile, "dev_pools", "", "Text file with a list of pools to use in dev env")

	flag.Parse()

	// if anything is still not set, die
	if cfg.AWSAccessKey != "" && cfg.PoolsFile != "" {
		log.Fatal("FATAL: Specify AWS config or dev config, not both")
	}
}
