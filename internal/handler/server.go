// Package handler 通过 Echo 提供本机 REST/SSE 服务，只处理传输、校验和错误映射。
// 采集、任务恢复和数据库事务交给对应的用例与适配器，避免业务规则散落在路由中。
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"quotes/internal/domain"
	"quotes/internal/service"
)

// JobUseCases 是传输层需要的任务接口，不暴露后台协程或具体仓库。
type JobUseCases interface {
	DefaultOutput() string
	Snapshot() *domain.TaskSnapshot
	Start(domain.DownloadOptions) (domain.TaskSnapshot, error)
	Cancel() error
	History() ([]domain.TaskSnapshot, error)
	Resume(string) (domain.TaskSnapshot, error)
}

// Dependencies 在组装根构造；Handler 只依赖用例，不构造存储、协议或 SDK。
type Dependencies struct {
	Jobs      JobUseCases               // 任务创建、取消和恢复。
	Workspace *service.WorkspaceService // 文件选择和线路检查用例。
	Preview   *service.PreviewService   // 原始归档预览用例。
	Warehouse *service.WarehouseService // 类型化研究查询、参考导入与快照用例。
	Proxy     *service.ProxyService     // 统一出站代理设置用例；nil 时禁用相关端点。
}
type Server struct {
	manager   JobUseCases               // 当前共享任务入口，HTTP 不拥有任务生命周期。
	workspace *service.WorkspaceService // 本机工作区能力，不接触具体文件系统实现。
	preview   *service.PreviewService   // 读取与复权编排，Handler 只负责响应序列化。
	warehouse *service.WarehouseService // 仓库用例，不暴露任何通用 Call 或 SQL。
	proxy     *service.ProxyService     // 代理设置用例；空表示该端点未装配。
}

func NewServer(d Dependencies) *Server {
	return &Server{manager: d.Jobs, workspace: d.Workspace, preview: d.Preview, warehouse: d.Warehouse, proxy: d.Proxy}
}

func (s *Server) Handler(frontendOrigins ...string) http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit("1M"))
	e.Use(securityHeaders)
	if len(frontendOrigins) > 0 {
		e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins: frontendOrigins,
			AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions},
			AllowHeaders: []string{echo.HeaderContentType},
		}))
	}
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		status := http.StatusInternalServerError
		message := err.Error()
		var httpError *echo.HTTPError
		if errors.As(err, &httpError) {
			status = httpError.Code
			message = fmt.Sprint(httpError.Message)
		}
		_ = c.JSON(status, map[string]string{"error": message})
	}

	e.GET("/api/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})
	e.GET("/api/defaults", s.handleDefaults)
	e.GET("/api/proxy", s.handleProxyGet)
	e.POST("/api/proxy", s.handleProxySet)
	e.GET("/api/task", s.handleTask)
	e.POST("/api/task", s.handleStart)
	e.DELETE("/api/task", s.handleCancel)
	e.POST("/api/hosts/test", s.handleHostTest)
	e.GET("/api/fs/browse", s.handleBrowse)
	e.POST("/api/fs/directories", s.handleCreateDirectory)
	e.GET("/api/preview", s.handlePreview)
	e.GET("/api/events", s.handleEvents)
	e.GET("/api/jobs", s.handleHistory)
	e.POST("/api/jobs/:id/resume", s.handleResume)
	e.GET("/api/warehouse", s.handleWarehouse)
	e.GET("/api/warehouse/bars", s.handleWarehouseBars)
	e.GET("/api/warehouse/datasets", s.handleDatasets)
	e.GET("/api/warehouse/gaps", s.handleGaps)
	e.POST("/api/warehouse/export", s.handleExport)
	e.POST("/api/warehouse/reference", s.handleReference)
	e.GET("/api/templates/:kind", s.handleTemplate)
	return e
}

func securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set("X-Content-Type-Options", "nosniff")
		c.Response().Header().Set("Referrer-Policy", "no-referrer")
		c.Response().Header().Set("X-Frame-Options", "DENY")
		return next(c)
	}
}

func (s *Server) handleDefaults(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"lineSource":       "builtin",
		"gbbqPath":         "",
		"outputDir":        s.manager.DefaultOutput(),
		"category":         9,
		"count":            800,
		"workers":          12,
		"requestRate":      domain.DefaultRequestRate,
		"hostRequestRate":  domain.DefaultHostRequestRate,
		"timeout":          8,
		"markets":          []int{0, 1, 2},
		"warehouseEnabled": s.warehouse.Enabled(),
		"task":             s.manager.Snapshot(),
	})
}

func (s *Server) handleTask(c echo.Context) error {
	return c.JSON(http.StatusOK, s.manager.Snapshot())
}

func (s *Server) handleStart(c echo.Context) error {
	var options domain.DownloadOptions
	if err := decodeJSON(c, &options); err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	snapshot, err := s.manager.Start(options)
	if err != nil {
		return writeError(c, http.StatusConflict, err)
	}
	return c.JSON(http.StatusAccepted, snapshot)
}

func (s *Server) handleCancel(c echo.Context) error {
	if err := s.manager.Cancel(); err != nil {
		return writeError(c, http.StatusConflict, err)
	}
	return c.JSON(http.StatusOK, s.manager.Snapshot())
}

type hostTestRequest struct {
	Timeout int `json:"timeout"` // 单端点协议请求超时秒数，0 默认 8，允许 2–60，不是全体线路总时限。
}

func (s *Server) handleHostTest(c echo.Context) error {
	var request hostTestRequest
	if err := decodeJSON(c, &request); err != nil {
		return writeError(c, http.StatusBadRequest, err)
	}
	report, err := s.workspace.TestLines(c.Request().Context(), request.Timeout)
	return respond(c, report, err, http.StatusServiceUnavailable)
}

// handleEvents 周期推送完整任务快照，而非必须逐条消费的增量事件。
// 浏览器重连即可取得当前状态；关闭页面只断开订阅，不取消后台采集任务。
func (s *Server) handleEvents(c echo.Context) error {
	response := c.Response()
	flusher, ok := response.Writer.(http.Flusher)
	if !ok {
		return writeError(c, http.StatusInternalServerError, fmt.Errorf("当前服务不支持事件流"))
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	send := func() error {
		payload, err := json.Marshal(s.manager.Snapshot())
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(response, "event: task\ndata: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush() // 每次推送立即刷新，避免进度被 HTTP 缓冲而长时间停留在旧状态。
		return nil
	}
	if err := send(); err != nil {
		return nil
	}
	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-ticker.C:
			if err := send(); err != nil {
				return nil
			}
		}
	}
}

// decodeJSON 限制请求体并拒绝未知字段，让前端拼错参数时明确失败，而不是静默使用默认值。
func decodeJSON(c echo.Context, target any) error {
	request := c.Request()
	request.Body = http.MaxBytesReader(c.Response().Writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("请求内容无效：%w", err)
	}
	return nil
}

func writeError(c echo.Context, status int, err error) error {
	return c.JSON(status, map[string]string{"error": err.Error()})
}

// respond 仅把领域错误分类映射为 HTTP，不在这里补业务默认值或执行存储操作。
func respond(c echo.Context, value any, err error, fallback int) error {
	if err == nil {
		return c.JSON(http.StatusOK, value)
	}
	status := fallback
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	return writeError(c, status, err)
}
