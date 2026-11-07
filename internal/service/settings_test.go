package service

import (
	"testing"

	"quotes/internal/domain"
)

type memoryProxyStore struct {
	loaded    domain.ProxySettings
	saved     []domain.ProxySettings
	saveError error
}

func (s *memoryProxyStore) Load() (domain.ProxySettings, error) { return s.loaded, nil }
func (s *memoryProxyStore) Save(settings domain.ProxySettings) error {
	if s.saveError != nil {
		return s.saveError
	}
	s.saved = append(s.saved, settings)
	s.loaded = settings
	return nil
}

func TestProxyServiceUpdateNormalizesAndPersists(t *testing.T) {
	store := &memoryProxyStore{}
	subject, err := NewProxyService(store)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := subject.Update("  SOCKS5://proxy.local  ")
	if err != nil {
		t.Fatal(err)
	}
	if updated.URL != "socks5://proxy.local:1080" {
		t.Fatalf("规范化结果 %q", updated.URL)
	}
	if len(store.saved) != 1 || store.saved[0].URL != updated.URL {
		t.Fatalf("持久化内容 %v", store.saved)
	}
	if subject.Current().URL != updated.URL {
		t.Fatalf("当前设置 %q", subject.Current().URL)
	}
}

func TestProxyServiceRejectsInvalidAddress(t *testing.T) {
	store := &memoryProxyStore{}
	subject, err := NewProxyService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subject.Update("ftp://proxy"); err == nil {
		t.Fatal("期望拒绝未知协议")
	}
	if len(store.saved) != 0 {
		t.Fatalf("拒绝的设置不应落盘：%v", store.saved)
	}
}

func TestProxyServiceClearRevertsToDirect(t *testing.T) {
	store := &memoryProxyStore{loaded: domain.ProxySettings{URL: "socks5://proxy:1080"}}
	subject, err := NewProxyService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subject.Update("   "); err != nil {
		t.Fatal(err)
	}
	if subject.Current().URL != "" {
		t.Fatalf("清空后仍为 %q", subject.Current().URL)
	}
}

func TestProxyServiceKeepsDirectOnCorruptStoredValue(t *testing.T) {
	store := &memoryProxyStore{loaded: domain.ProxySettings{URL: "not a url"}}
	subject, err := NewProxyService(store)
	if err != nil {
		t.Fatal(err)
	}
	if subject.Current().URL != "" {
		t.Fatalf("损坏存量未回退直连：%q", subject.Current().URL)
	}
}

func TestProxyServiceSeedAppliesOnlyWithoutSavedSetting(t *testing.T) {
	empty := &memoryProxyStore{}
	subject, err := NewProxyService(empty)
	if err != nil {
		t.Fatal(err)
	}
	subject.Seed(domain.ProxySettings{URL: "socks5://env:1080"})
	if subject.Current().URL != "socks5://env:1080" {
		t.Fatalf("无保存设置时应采用环境变量初始值：%q", subject.Current().URL)
	}
	if len(empty.saved) != 0 {
		t.Fatalf("Seed 不应落盘：%v", empty.saved)
	}

	saved := &memoryProxyStore{loaded: domain.ProxySettings{URL: "socks5://ui:1080"}}
	other, err := NewProxyService(saved)
	if err != nil {
		t.Fatal(err)
	}
	other.Seed(domain.ProxySettings{URL: "http://env:80"})
	if other.Current().URL != "socks5://ui:1080" {
		t.Fatalf("已保存设置应优先于环境变量初始值：%q", other.Current().URL)
	}
}
