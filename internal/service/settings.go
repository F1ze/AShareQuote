package service

import (
	"fmt"
	"sync"

	"quotes/internal/domain"
)

// ProxyStore 持久化代理设置；存储格式与位置由仓库决定，服务不读写文件。
// Save 语义为整份设置原子替换，成功返回后设置才算生效。
type ProxyStore interface {
	Load() (domain.ProxySettings, error)
	Save(domain.ProxySettings) error
}

// ProxyService 校验并发布统一的出站代理设置；校验规则在领域，存储在仓库。
// 组装根可通过包装 ProxyStore 在保存后热应用（更新拨号器、重启数据会话）。
type ProxyService struct {
	mu      sync.RWMutex
	store   ProxyStore
	current domain.ProxySettings
}

// NewProxyService 启动时加载一次设置；存量损坏、缺失或未过校验时按直连处理。
func NewProxyService(store ProxyStore) (*ProxyService, error) {
	current, err := store.Load()
	if err != nil {
		return nil, err
	}
	// 服务不信任仓库返回的内容；越界存量回退直连，不阻断启动。
	normalized, invalid := domain.ParseProxyURL(current.URL)
	if invalid != nil {
		normalized = domain.ProxySettings{}
	}
	return &ProxyService{store: store, current: normalized}, nil
}

// Seed 提供运行期初始设置：仅在没有任何已保存设置时生效，不落盘。
// 用于按通用约定从 ALL_PROXY 等环境变量读取的初始值；界面保存的设置始终优先。
func (s *ProxyService) Seed(settings domain.ProxySettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.URL == "" {
		s.current = settings
	}
}

// Current 返回当前生效的代理设置；空 URL 表示直连。
func (s *ProxyService) Current() domain.ProxySettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Update 校验、持久化并发布新设置；返回规范化结果。
// 持久化成功即视为生效，运行中的旧连接不做中断，只约束之后的新连接。
func (s *ProxyService) Update(raw string) (domain.ProxySettings, error) {
	settings, err := domain.ParseProxyURL(raw)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	if err := s.store.Save(settings); err != nil {
		return domain.ProxySettings{}, fmt.Errorf("保存代理设置：%w", err)
	}
	s.mu.Lock()
	s.current = settings
	s.mu.Unlock()
	return settings, nil
}
