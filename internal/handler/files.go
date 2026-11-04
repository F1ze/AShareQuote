package handler

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func (s *Server) handleBrowse(c echo.Context) error {
	result, err := s.workspace.Browse(c.QueryParam("path"), c.QueryParam("kind"))
	return respond(c, result, err, http.StatusBadRequest)
}

type createDirectoryRequest struct {
	Path string `json:"path"` // 已有父目录；空值时使用默认输出目录。
	Name string `json:"name"` // 新建单层目录名，禁止 .、.. 和路径分隔符，不能借此传入嵌套路径。
}

func (s *Server) handleCreateDirectory(c echo.Context) error {
	var request createDirectoryRequest
	if err := decodeJSON(c, &request); err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	path, err := s.workspace.CreateDirectory(request.Path, request.Name)
	if err != nil {
		return respond(c, nil, err, http.StatusBadRequest)
	}
	return c.JSON(http.StatusCreated, map[string]string{"path": path})
}
