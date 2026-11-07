package repository

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"quotes/internal/domain"
)

// SettingsStore 将应用级设置保存为数据目录中的单个 JSON 文件。
// 读取走领域校验，损坏或不合规的存量内容按直连处理，不阻断应用启动。
type SettingsStore struct {
	Path string // 设置文件绝对或相对路径；目录须已存在或可创建。
}

func NewSettingsStore(path string) *SettingsStore {
	return &SettingsStore{Path: path}
}

type settingsFile struct {
	Proxy string `json:"proxy"` // 规范化后的代理地址；空表示直连。
}

func (s *SettingsStore) Load() (domain.ProxySettings, error) {
	raw, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return domain.ProxySettings{}, nil
	}
	if err != nil {
		return domain.ProxySettings{}, fmt.Errorf("读取设置文件：%w", err)
	}
	var stored settingsFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		// 内容损坏时不让应用拒绝启动：回退直连，下次保存会重写整个文件。
		return domain.ProxySettings{}, nil
	}
	settings, err := domain.ParseProxyURL(stored.Proxy)
	if err != nil {
		return domain.ProxySettings{}, nil
	}
	return settings, nil
}

func (s *SettingsStore) Save(settings domain.ProxySettings) error {
	if settings.URL != "" {
		// 双重保险：入口已校验过，存储边界再次拒绝未知内容。
		normalized, err := domain.ParseProxyURL(settings.URL)
		if err != nil {
			return err
		}
		settings = normalized
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return fmt.Errorf("创建设置目录：%w", err)
	}
	payload, err := json.MarshalIndent(settingsFile{Proxy: settings.URL}, "", "  ")
	if err != nil {
		return err
	}
	// 先写临时文件再原子替换，避免半写状态被后续启动读取。
	temporary := s.Path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0600); err != nil {
		return fmt.Errorf("写入设置文件：%w", err)
	}
	if err := os.Rename(temporary, s.Path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("替换设置文件：%w", err)
	}
	return nil
}
