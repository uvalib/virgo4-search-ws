package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/gin-gonic/gin"
)

// NewPool creates a new pool with the specified private URL. It
// is initialy not active and has no name, description nor publicURL
func NewPool(privateURL string) Pool {
	return Pool{PrivateURL: privateURL,
		Alive: false, FallbackLanguage: "en-US",
		Translations: make(map[string]PoolDesc)}
}

// Pool defines the attributes of a search pool. Pools are initially registered
// with only a PrivateURL. Full details are read from the /identify endpoint.
type Pool struct {
	PrivateURL       string
	PublicURL        string
	Alive            bool
	FallbackLanguage string

	// Translations is a map of pool descriptive info that has been
	// translated to other languages. Language identifier is the key
	Translations map[string]PoolDesc
}

// PoolDesc contains the language-specific name and description of a pool
type PoolDesc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// HasIdentity returns true if the pool has been identified in the tgt language
func (p *Pool) HasIdentity(language string) bool {
	_, ok := p.Translations[language]
	return ok
}

// GetIdentity returns identify information in the target language. If not availble,
// return the fallback identity. If nothing is available, nil is returned
func (p *Pool) GetIdentity(language string) *PoolDesc {
	if desc, ok := p.Translations[language]; ok {
		return &desc
	}
	if desc, ok := p.Translations[p.FallbackLanguage]; ok {
		return &desc
	}
	return nil
}

// Identify will call the pool /identify endpoint to get full pool details.
func (p *Pool) Identify(language string) *PoolDesc {
	log.Printf("Identify %s with Accept-Language %s", p.PrivateURL, language)
	desc := p.GetIdentity(language)
	if desc != nil {
		log.Printf("%s already identified in %s as %s", p.PrivateURL, language, desc.Name)
		return desc
	}

	fallbackDesc := p.GetIdentity(p.FallbackLanguage)
	timeout := time.Duration(2 * time.Second)
	client := http.Client{
		Timeout: timeout,
	}
	URL := fmt.Sprintf("%s/identify", p.PrivateURL)
	idRequest, _ := http.NewRequest("GET", URL, nil)
	idRequest.Header.Set("Accept-Language", language)
	resp, err := client.Do(idRequest)
	if err != nil {
		log.Printf("ERROR: %s /identify failed: %s", p.PrivateURL, err.Error())
		return fallbackDesc
	}

	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("ERROR: %s/identify returned bad status code : %d: ", p.PrivateURL, resp.StatusCode)
		return fallbackDesc
	}

	type idResp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		PublicURL   string `json:"public_url"`
	}
	var identity idResp
	respTxt, _ := ioutil.ReadAll(resp.Body)
	json.Unmarshal(respTxt, &identity)
	p.PublicURL = identity.PublicURL
	newDesc := PoolDesc{Name: identity.Name, Description: identity.Description}
	log.Printf("Adding %s translation %s:%s for %s", language, identity.Name,
		identity.Description, p.PrivateURL)
	p.Translations[language] = newDesc
	return &newDesc
}

// Ping will check the health of a pool by calling /healthcheck and looking for good status
func (p *Pool) Ping() error {
	timeout := time.Duration(1500 * time.Millisecond)
	client := http.Client{
		Timeout: timeout,
	}
	hcURL := fmt.Sprintf("%s/healthcheck", p.PrivateURL)
	resp, err := client.Get(hcURL)
	if err != nil {
		log.Printf("ERROR: %s ping failed: %s", p.PrivateURL, err.Error())
		p.Alive = false
		return err
	}

	defer resp.Body.Close()
	respTxt, _ := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("   * FAIL: %s returned bad status code : %d: ", p.PrivateURL, resp.StatusCode)
		p.Alive = false
		return fmt.Errorf("%d:%s", resp.StatusCode, respTxt)
	}

	if strings.Contains(string(respTxt), "false") {
		log.Printf("   * FAIL: %s has unhealthy components", p.PrivateURL)
		p.Alive = false
		return fmt.Errorf("%s", respTxt)
	}

	p.Alive = true
	return nil
}

// GetPools gets a list of all active pools and returns it as JSON. This
// request will also pull the Accept-Language header and use it to call (if necessary)
// pool.Identify to get the name/description for each pool in the proper language.
// Identification info is cached in-memory under the taget language.
func (svc *ServiceContext) GetPools(c *gin.Context) {
	if len(svc.Pools) == 0 {
		log.Printf("No pools available")
		c.JSON(http.StatusOK, make([]*Pool, 0, 1))
		return
	}

	// define the public data which describes a pool
	type poolInfo struct {
		PublicURL   string `json:"url"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	// Pick the first option in Accept-Language header - or en-US if none
	acceptLang := strings.Split(c.GetHeader("Accept-Language"), ",")[0]
	if acceptLang == "" {
		acceptLang = "en-US"
	}

	log.Printf("Get %s active pool info", acceptLang)
	active := make([]poolInfo, 0)
	for _, p := range svc.Pools {
		if p.Alive {
			// NOTES: each in-memory pool tracks name/desc inf pairs in a map
			// keyed by language. If the requested translation doesn't exist,
			// look it up and cache the results. All pools have a fallback translation
			// that is popuated upon registration. Use that if other translate fails.
			pi := poolInfo{PublicURL: p.PublicURL}
			desc := p.GetIdentity(acceptLang)
			if desc == nil {
				p.Identify(acceptLang)
				desc = p.GetIdentity(acceptLang)
			}
			pi.Name = desc.Name
			pi.Description = desc.Description
			active = append(active, pi)
		}
	}
	c.JSON(http.StatusOK, active)
}

// PoolExists checks if a pool with the given URL exists, regardless of the current status.
func (svc *ServiceContext) PoolExists(url string) bool {
	for _, p := range svc.Pools {
		if p.PrivateURL == url || p.PublicURL == url {
			return true
		}
	}
	return false
}

// IsPoolActive checks if a pool with the specified URL is registered and alive
func (svc *ServiceContext) IsPoolActive(url string) bool {
	for _, pool := range svc.Pools {
		if (pool.PrivateURL == url || pool.PublicURL == url) && pool.Alive {
			return true
		}
	}
	return false
}

// UpdateAuthoritativePools fetches a list of current pools from a DynamoDB. New pools
// will be added to an in-memory cache. If an existing pool is not found in the
// list, it will be removed from service.
func (svc *ServiceContext) UpdateAuthoritativePools() error {
	if svc.DevPoolsFile != "" {
		svc.LoadDevPools()
		return nil
	}
	log.Printf("Scanning for pool updates in %s", svc.PoolsTable)
	params := dynamodb.ScanInput{
		TableName: aws.String(svc.PoolsTable),
	}
	result, err := svc.DynamoDB.Scan(&params)
	if err != nil {
		log.Printf("ERROR: Unable to retrieve pools from AWS: %v", err)
		return err
	}

	// NOTE: This structure matches the only attribute value in the DynamoDB table
	type Item struct {
		URL string
	}
	var authoritativeURLs []string
	for _, ddbItem := range result.Items {
		item := Item{}
		err = dynamodbattribute.UnmarshalMap(ddbItem, &item)
		if err != nil {
			log.Printf("Unable to read DDB item %v: %v", ddbItem, err)
		} else {
			authoritativeURLs = append(authoritativeURLs, item.URL)
			if svc.PoolExists(item.URL) {
				// pool already exists; no nothing
				continue
			}
			log.Printf("Authoritative pools update found new pool URL %s", item.URL)
			svc.AddPool(item.URL)
		}
	}

	// Now see if there are any pools in memory that are no longer in the
	// authoritatve list, they have been retired and should be dropped
	svc.PrunePools(authoritativeURLs)
	return nil
}

// LoadDevPools is only used in local development mode. It will fetch a static list of
// pools from a text file. These pools will be pinged for health, but not updated.
func (svc *ServiceContext) LoadDevPools() {
	log.Printf("Load pools from dev mode pools file %s", svc.DevPoolsFile)
	data, _ := ioutil.ReadFile(svc.DevPoolsFile)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var authoritativeURLs []string
	for scanner.Scan() {
		svcURL := scanner.Text()
		authoritativeURLs = append(authoritativeURLs, svcURL)
		if svc.PoolExists(svcURL) {
			continue
		}
		log.Printf("Authoritative pools update found new pool URL %s", svcURL)
		svc.AddPool(svcURL)
	}
	svc.PrunePools(authoritativeURLs)
}

// AddPool will create a new pool, ping it and add it to the in-memory pool cache if successful.
// Pools are initially identified with default language en-US.
func (svc *ServiceContext) AddPool(privateURL string) {
	pool := NewPool(privateURL)
	if err := pool.Ping(); err != nil {
		log.Printf("   * %s is not available: %s", pool.PrivateURL, err.Error())
	} else {
		desc := pool.Identify("en-US")
		if desc != nil {
			desc := pool.GetIdentity("en-US")
			log.Printf("   * %s is alive and identified (en-US) as %s", pool.PrivateURL, desc.Name)
			svc.Pools = append(svc.Pools, &pool)
		} else {
			log.Printf("   * %s is alive, but failed identify", pool.PrivateURL)
			svc.Pools = append(svc.Pools, &pool)
		}
	}
}

// PrunePools compares the in-memory pools with the authoritative pool list. Any
// pools that are not on the authoritative list are removed.
func (svc *ServiceContext) PrunePools(authoritativeURLs []string) {
	for idx := len(svc.Pools) - 1; idx >= 0; idx-- {
		p := svc.Pools[idx]
		found := false
		for _, authURL := range authoritativeURLs {
			if authURL == p.PrivateURL || authURL == p.PublicURL {
				found = true
				break
			}
		}
		if found == false {
			log.Printf("Pool %s is no longer on the authoritative list. Removing", p.PrivateURL)
			svc.Pools = append(svc.Pools[:idx], svc.Pools[idx+1:]...)
		}
	}
}
