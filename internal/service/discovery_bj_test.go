package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type bjCatalogClient struct {
	securities   []Security
	err          error
	standardCall bool
}

func (c *bjCatalogClient) Close() {}
func (c *bjCatalogClient) SecurityCount(uint8) (int, error) {
	c.standardCall = true
	return 0, errors.New("北交所不应调用标准数量命令")
}
func (c *bjCatalogClient) SecurityList(uint8, int, int) ([]Security, error) {
	c.standardCall = true
	return nil, errors.New("北交所不应调用标准列表命令")
}
func (c *bjCatalogClient) SecurityBars(int, uint8, string, int, int) ([]Bar, error) {
	return nil, nil
}
func (c *bjCatalogClient) BeijingSecurities() ([]Security, error) {
	return c.securities, c.err
}

func TestDiscoverMarketUsesDedicatedBJCatalog(t *testing.T) {
	client := &bjCatalogClient{securities: []Security{{Market: 2, Code: "920000", Name: "安徽凤凰"}}}
	task := &jobRun{ctx: context.Background(), hosts: []Host{{IP: "127.0.0.1"}}, newClient: func([]Host, time.Duration, int) Client { return client }}
	securities, err := task.discoverMarket(2)
	if err != nil || len(securities) != 1 || securities[0].Code != "920000" {
		t.Fatalf("北交所专用码表未生效：%+v %v", securities, err)
	}
	if client.standardCall {
		t.Fatal("北交所调用了会超时的标准码表命令")
	}
}

func TestDiscoverMarketRejectsBJCatalogFailure(t *testing.T) {
	for _, client := range []*bjCatalogClient{{err: errors.New("下载失败")}, {securities: nil}} {
		task := &jobRun{ctx: context.Background(), hosts: []Host{{IP: "127.0.0.1"}}, newClient: func([]Host, time.Duration, int) Client { return client }}
		if _, err := task.discoverMarket(2); err == nil {
			t.Fatal("北交所码表失败或空目录被接受")
		}
	}
}
