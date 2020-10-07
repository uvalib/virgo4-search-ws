package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/signintech/gopdf"
)

type requestItem struct {
	Pool       string `json:"pool"`
	Identifier string `json:"identifier"`
}
type pdfRequest struct {
	Title string        `json:"title"`
	Notes string        `json:"notes"`
	Items []requestItem `json:"items"`
}

type itemDetail struct {
	Identifier string
	Title      []string
	Author     []string
	Library    []string
	Location   []string
	CallNumber []string
	StatusCode int
	Message    string
	ElapsedMS  int64
}

// GeneratePDF accepts a list of objects containg pool and identifer as POST data
// It will generate a PDF containing details about the items that can be used to help find
// the items in the stacks
func (svc *ServiceContext) GeneratePDF(c *gin.Context) {
	var req pdfRequest
	if err := c.BindJSON(&req); err != nil {
		log.Printf("ERROR: Unable to parse PDF request: %s", err.Error())
		c.String(http.StatusBadRequest, "Invalid PDF request")
		return
	}

	acceptLang := c.GetHeader("Accept-Language")
	if acceptLang == "" {
		acceptLang = "en-US"
	}

	headers := map[string]string{
		"Content-Type":    "application/json",
		"Accept-Language": acceptLang,
		"Authorization":   c.GetHeader("Authorization"),
	}

	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4}) // W: 595, H: 842
	pdf.AddPage()
	err := pdf.AddTTFFont("osr", "./ttf/OpenSans-Regular.ttf")
	if err != nil {
		log.Printf("ERROR: Unable to load PDF font %s", err.Error())
		c.String(http.StatusInternalServerError, "Unable to generate PDF")
		return
	}
	err = pdf.AddTTFFont("osb", "./ttf/OpenSans-Bold.ttf")
	if err != nil {
		log.Printf("ERROR: Unable to load PDF bold font %s", err.Error())
		c.String(http.StatusInternalServerError, "Unable to generate PDF")
		return
	}

	start := time.Now()
	pools, err := svc.lookupPools(acceptLang)
	if err != nil {
		log.Printf("ERROR: Unable to get pools for PDF lookup: %+v", err)
		c.String(http.StatusInternalServerError, "Unable to find item details")
		return
	}

	// Kick off all pool requests in parallel and wait for all to respond
	channel := make(chan *itemDetail)
	outstandingRequests := 0
	for _, item := range req.Items {
		outstandingRequests++
		pool := getPool(pools, item.Pool)
		if pool == nil {
			log.Printf("ERROR: Pool %s not found - Skipping", item.Pool)
		}
		go svc.getDetails(item, pool, headers, channel)
	}

	out := make([]*itemDetail, 0)
	for outstandingRequests > 0 {
		itemResp := <-channel
		if itemResp.StatusCode == http.StatusOK {
			out = append(out, itemResp)
		} else {
			log.Printf("ERROR: unable to get details for %s: %s", itemResp.Identifier, itemResp.Message)
		}
		outstandingRequests--
	}

	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	log.Printf("SUCCESS: All item details for printout receieved in %dms", elapsedMS)

	// render the PDF...
	startY := 0
	if req.Title != "" {
		pdf.SetFont("osb", "", 12)
		pdf.Cell(nil, req.Title)
		pdf.Br(18)
		startY += 18
	}
	if req.Notes != "" {
		pdf.SetFont("osr", "", 10)
		pdf.SetX(20)
		pdf.Cell(nil, req.Notes)
		pdf.Br(18)
		startY += 18
	}
	if startY > 0 {
		pdf.Line(10, float64(startY+10), 585, float64(startY+10))
		pdf.Br(15)
		startY += 15
	}
	for _, item := range out {
		pdf.SetFont("osb", "", 10)
		pdf.Cell(nil, strings.Join(item.Title, "; "))
		pdf.Br(18)
		pdf.SetFont("osr", "", 10)
		pdf.SetX(20)
		pdf.Cell(nil, strings.Join(item.Author, "; "))
		pdf.Br(18)
		pdf.SetX(20)
		pdf.Cell(nil, strings.Join(item.Library, "; "))
		pdf.Br(18)
		pdf.SetX(20)
		pdf.Cell(nil, strings.Join(item.Location, "; "))
		pdf.Br(18)
		pdf.SetX(20)
		pdf.Cell(nil, strings.Join(item.CallNumber, ", "))
		pdf.Br(25)
	}

	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Disposition", "attachment; filename=results.pdf")
	pdf.Write(c.Writer)
}

func getPool(pools []*pool, identifier string) *pool {
	for _, p := range pools {
		if p.V4ID.URL == identifier || p.V4ID.ID == identifier {
			return p
		}
	}
	return nil
}

func (svc *ServiceContext) getDetails(item requestItem, pool *pool, headers map[string]string, channel chan *itemDetail) {
	url := fmt.Sprintf("%s/api/resource/%s", pool.PrivateURL, item.Identifier)
	resp := serviceRequest("GET", url, nil, headers, svc.HTTPClient)
	respItem := &itemDetail{StatusCode: resp.StatusCode, ElapsedMS: resp.ElapsedMS, Identifier: item.Identifier}
	if respItem.StatusCode != http.StatusOK {
		channel <- respItem
		return
	}

	type parsedField struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	var parsedResp struct {
		Fields []parsedField `json:"fields"`
	}

	err := json.Unmarshal(resp.Response, &parsedResp)
	if err != nil {
		log.Printf("ERROR: Unable to parse response %+v", err)
		respItem.StatusCode = http.StatusInternalServerError
		respItem.Message = "Malformed search response"
		channel <- respItem
		return
	}

	for _, field := range parsedResp.Fields {
		if field.Type == "title" {
			respItem.Title = append(respItem.Title, field.Value)
		}
		if field.Name == "author" {
			respItem.Author = append(respItem.Author, field.Value)
		}
		if field.Name == "library" {
			respItem.Library = append(respItem.Library, field.Value)
		}
		if field.Name == "location" {
			respItem.Location = append(respItem.Location, field.Value)
		}
		if field.Name == "call_number" {
			respItem.CallNumber = append(respItem.CallNumber, field.Value)
		}
	}

	channel <- respItem
}
