package handler

import (
	"fmt"
	"strconv"

	"github.com/labstack/echo/v4"

	"quotes/internal/domain"
)

func (s *Server) handleWarehouse(c echo.Context) error {
	v, err := s.warehouse.Summary(c.Request().Context())
	return respond(c, v, err, 503)
}
func (s *Server) handleExport(c echo.Context) error {
	v, err := s.warehouse.Export(c.Request().Context())
	return respond(c, v, err, 503)
}
func (s *Server) handleGaps(c echo.Context) error {
	v, err := s.warehouse.Gaps(c.Request().Context())
	return respond(c, v, err, 503)
}
func (s *Server) handleDatasets(c echo.Context) error {
	page := 1
	if value := c.QueryParam("page"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return writeError(c, 400, err)
		}
		page = n
		if page == 0 {
			return writeError(c, 400, domain.ErrInvalidInput)
		}
	}
	v, err := s.warehouse.Datasets(c.Request().Context(), domain.DatasetQuery{Text: c.QueryParam("q"), Period: c.QueryParam("period"), Page: page})
	return respond(c, v, err, 503)
}
func (s *Server) handleHistory(c echo.Context) error {
	v, err := s.manager.History()
	return respond(c, v, err, 500)
}
func (s *Server) handleResume(c echo.Context) error {
	v, err := s.manager.Resume(c.Param("id"))
	if err != nil {
		return writeError(c, 409, err)
	}
	return c.JSON(202, v)
}
func (s *Server) handleReference(c echo.Context) error {
	var request domain.ReferenceImport
	if err := decodeJSON(c, &request); err != nil {
		return writeError(c, 400, err)
	}
	v, err := s.warehouse.ImportReference(c.Request().Context(), request)
	return respond(c, v, err, 503)
}
func (s *Server) handleWarehouseBars(c echo.Context) error {
	limit := 200
	if value := c.QueryParam("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return writeError(c, 400, err)
		}
		limit = n
		if limit == 0 {
			return writeError(c, 400, domain.ErrInvalidInput)
		}
	}
	v, err := s.warehouse.Bars(c.Request().Context(), domain.BarQuery{Before: c.QueryParam("before"), Exchange: c.QueryParam("exchange"), Code: c.QueryParam("code"), Period: c.QueryParam("period"), From: c.QueryParam("from"), To: c.QueryParam("to"), Limit: limit})
	return respond(c, v, err, 503)
}
func (s *Server) handleTemplate(c echo.Context) error {
	var content string
	switch c.Param("kind") {
	case "instruments":
		content = "exchange,code,name,listing_date,delisting_date,status\nSH,600000,浦发银行,1999-11-10,,listed\n"
	case "calendar":
		content = "exchange,date,is_open\nSH,2024-01-02,1\nSZ,2024-01-02,1\nBJ,2024-01-02,1\n"
	case "bars":
		content = "date,open,high,low,close,volume,amount\n2024-01-02,10,11,9,10.5,100,105000\n"
	default:
		return writeError(c, 404, fmt.Errorf("模板不存在"))
	}
	c.Response().Header().Set("Content-Disposition", "attachment; filename="+c.Param("kind")+".csv")
	return c.Blob(200, "text/csv; charset=utf-8", []byte("\xef\xbb\xbf"+content))
}
