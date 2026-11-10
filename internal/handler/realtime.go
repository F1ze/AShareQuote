package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"quotes/internal/domain"
	"quotes/internal/service"
)

func (s *Server) handleRealtime(c echo.Context) error {
	stocks, selected, err := service.ParseRealtimeSelection(c.QueryParam("codes"), c.QueryParam("selected"))
	if err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	if s.realtime == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "实时行情未启用")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 8*time.Second)
	defer cancel()
	var result domain.RealtimeSnapshot
	switch c.QueryParam("details") {
	case "", "true":
		result, err = s.realtime.Snapshot(ctx, stocks, selected)
	case "false":
		result, err = s.realtime.Quotes(ctx, stocks, selected)
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "details 须为 true 或 false")
	}
	if err != nil {
		return writeError(c, http.StatusBadGateway, err)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, result)
}

func (s *Server) handleRealtimeWatchGet(c echo.Context) error {
	if s.realtime == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "实时行情未启用")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, s.realtime.Watch())
}
func (s *Server) handleRealtimeWatchSet(c echo.Context) error {
	if s.realtime == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "实时行情未启用")
	}
	var watch domain.RealtimeWatch
	if err := decodeJSON(c, &watch); err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	if err := s.realtime.SetWatch(watch); err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	return c.JSON(http.StatusOK, s.realtime.Watch())
}
