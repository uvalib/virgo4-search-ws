package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
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
type exportRequest struct {
	Title string        `json:"title"`
	Notes string        `json:"notes"`
	Items []requestItem `json:"items"`
}

type itemDetail struct {
	Identifier string
	Pool       string
	CallNumber []string
	Date       string
	Title      []string
	Author     []string
	Library    []string
	Location   []string
	Format     []string
	StatusCode int
	Message    string
	ElapsedMS  int64
}

// GenerateCSV accepts a list of objects containg pool and identifer as POST data
// It will generate CSV containing details about the items
func (svc *ServiceContext) GenerateCSV(c *gin.Context) {
	var req exportRequest
	if err := c.BindJSON(&req); err != nil {
		log.Printf("ERROR: Unable to parse CSV request: %s", err.Error())
		c.String(http.StatusBadRequest, "Invalid CSV request")
		return
	}

	// Notes is used to pass in the base URL of the request. Need
	// it to generate the full item details URL
	if req.Notes == "" {
		log.Printf("ERROR: Missing required notes field")
		c.String(http.StatusBadRequest, "Invalid CSV request")
		return
	}

	start := time.Now()
	details, err := svc.lookupItems(c, req.Items)
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	if err != nil {
		log.Printf("ERROR: Unable to get CSV item details: %s", err.Error())
		c.String(http.StatusNotFound, "Unable to find item details")
		return
	}
	log.Printf("SUCCESS: All item details for CSV receieved in %dms", elapsedMS)
	c.Header("Content-Type", "text/csv")
	cw := csv.NewWriter(c.Writer)
	csvHead := []string{"title", "author", "library", "location", "call number", "format", "date", "url"}
	cw.Write(csvHead)
	baseURL := req.Notes
	for _, item := range details {
		url := fmt.Sprintf("%s/sources/%s/items/%s", baseURL, item.Pool, item.Identifier)
		line := []string{
			strings.Join(item.Title, "; "),
			strings.Join(item.Author, "; "),
			strings.Join(item.Library, "; "),
			strings.Join(item.Location, "; "),
			strings.Join(item.CallNumber, "; "),
			strings.Join(item.Format, "; "),
			item.Date,
			url,
		}
		cw.Write(line)
	}

	cw.Flush()
}

func (svc *ServiceContext) lookupItems(c *gin.Context, items []requestItem) ([]*itemDetail, error) {
	// Pools have already been placed in request context by poolsMiddleware. Get them or fail
	pools := getPoolsFromContext(c)
	if len(pools) == 0 {
		return nil, errors.New("No pools found")
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

	// Kick off all pool requests in parallel and wait for all to respond
	channel := make(chan *itemDetail)
	outstandingRequests := 0
	for _, item := range items {
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

	return out, nil
}

// GeneratePDF accepts a list of objects containg pool and identifer as POST data
// It will generate a PDF containing details about the items that can be used to help find
// the items in the stacks
func (svc *ServiceContext) GeneratePDF(c *gin.Context) {
	var req exportRequest
	if err := c.BindJSON(&req); err != nil {
		log.Printf("ERROR: Unable to parse PDF request: %s", err.Error())
		c.String(http.StatusBadRequest, "Invalid PDF request")
		return
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
	out, err := svc.lookupItems(c, req.Items)
	elapsed := time.Since(start)
	elapsedMS := int64(elapsed / time.Millisecond)
	if err != nil {
		log.Printf("ERROR: Unable to get PDF item details: %s", err.Error())
		c.String(http.StatusNotFound, "Unable to find item details")
		return
	}
	log.Printf("SUCCESS: All item details for printout receieved in %dms", elapsedMS)

	// render the PDF..
	yPos := 20
	if req.Title != "" {
		yPos = renderLine(&pdf, 20, yPos, req.Title, "osb", 12)
	}
	if req.Notes != "" {
		yPos += 5
		yPos = renderLine(&pdf, 20, yPos, req.Notes, "osr", 10)
	}
	if yPos > 20 {
		yPos += 8
		pdf.Line(10, float64(yPos), 585, float64(yPos))
		yPos += 15
	}

	for _, item := range out {
		pdf.SetFont("osb", "", 10)
		yPos = renderLine(&pdf, 20, yPos, strings.Join(item.Title, "; "), "osb", 10)
		yPos = renderLine(&pdf, 30, yPos, strings.Join(item.Author, "; "), "osr", 10)
		yPos = renderLine(&pdf, 30, yPos, strings.Join(item.Location, "; "), "osr", 10)
		yPos = renderLine(&pdf, 30, yPos, strings.Join(item.CallNumber, "; "), "osr", 10)
		yPos += 10
	}

	c.Header("Content-Disposition", "attachment; filename=results.pdf")
	c.Header("Content-Type", "application/pdf")
	pdf.Write(c.Writer)
}

// render a line of the PDF with line breaks. return the new Y position
func renderLine(pdf *gopdf.GoPdf, xPos int, yPos int, line string, fontName string, fontSize int) int {
	pdf.SetFont(fontName, "", fontSize)
	words := strings.Fields(line)
	line = ""
	for _, word := range words {
		testLine := line
		if testLine != "" {
			testLine += " "
		}
		testLine += word
		lineW, _ := pdf.MeasureTextWidth(testLine)
		if lineW >= 550 {
			if yPos+(fontSize+6)+20 > int(gopdf.PageSizeA4.H) {
				pdf.AddPage()
				yPos = 20
			}
			pdf.SetY(float64(yPos))
			pdf.SetX(float64(xPos))
			pdf.Cell(nil, line)
			yPos += (fontSize + 6)
			line = word
		} else {
			line = testLine
		}
	}
	if line != "" {
		if yPos+(fontSize+6)+20 > int(gopdf.PageSizeA4.H) {
			pdf.AddPage()
			yPos = 20
		}
		pdf.SetY(float64(yPos))
		pdf.SetX(float64(xPos))
		pdf.Cell(nil, line)
		yPos += (fontSize + 4)
	}
	return yPos
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
	respItem := &itemDetail{StatusCode: resp.StatusCode, ElapsedMS: resp.ElapsedMS, Identifier: item.Identifier, Pool: pool.V4ID.ID}
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
		if field.Name == "format" {
			respItem.Format = append(respItem.Format, field.Value)
		}
		if field.Name == "published_date" {
			respItem.Date = field.Value
		}
		if field.Name == "location" {
			if field.Value != "By Request" {
				respItem.Location = append(respItem.Location, field.Value)
			}
		}
		if field.Name == "call_number" {
			respItem.CallNumber = append(respItem.CallNumber, field.Value)
		}
	}

	channel <- respItem
}
