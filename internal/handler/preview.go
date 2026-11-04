package handler

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"quotes/internal/domain"
)

// handlePreview 只将查询参数转成类型化用例输入，读取文件、复权及截窗全部由服务完成。
func (s *Server) handlePreview(c echo.Context) error {
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
	result, err := s.preview.Preview(c.Request().Context(), domain.PreviewQuery{File: c.QueryParam("file"), Mode: c.QueryParam("mode"), Limit: limit})
	return respond(c, result, err, http.StatusInternalServerError)
}
