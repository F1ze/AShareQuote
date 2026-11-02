package lines

import (
	"context"
	"errors"
	"testing"
	"time"

	"quotes/internal/core"
	"quotes/internal/sitecfg"
)

type fakeSmoke struct {
	count int
	ticks int
	err   error
}

func (f *fakeSmoke) Close()                           {}
func (f *fakeSmoke) SecurityCount(uint8) (int, error) { return f.count, f.err }
func (f *fakeSmoke) TransactionData(uint8, string, int, int) ([]core.Transaction, error) {
	if f.err != nil {
		return nil, f.err
	}
	return make([]core.Transaction, f.ticks), nil
}

func TestBuiltinRequiresRealBusinessData(t *testing.T) {
	spec := sitecfg.SeedSpec()
	spec.Hosts = spec.Hosts[:2]
	spec.TryHosts = nil
	provider := NewBuiltinWithSpec(func() sitecfg.Spec { return spec }, func(host core.Host, _ time.Duration) SmokeClient {
		if host.IP == spec.Hosts[0].IP {
			return &fakeSmoke{count: 5000, ticks: 5}
		}
		return &fakeSmoke{count: 5000, ticks: 0}
	})
	hosts, report, err := provider.Available(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !report.ConfigChecked || report.ConfigBytes == 0 || report.Available != 1 || len(hosts) != 1 {
		t.Fatalf("业务自检结果错误：%+v hosts=%v", report, hosts)
	}
	if hosts[0].Address() != report.Results[0].Address || hosts[0].IP != spec.Hosts[0].IP {
		t.Fatalf("可用线路未按检测结果正确排序：%v %+v", hosts, report.Results)
	}
}

func TestBuiltinRejectsHandshakeOnlySuccess(t *testing.T) {
	provider := NewBuiltin(func(core.Host, time.Duration) SmokeClient {
		return &fakeSmoke{err: errors.New("business response missing")}
	})
	_, report, err := provider.Available(context.Background(), time.Second)
	if err == nil || report.Available != 0 {
		t.Fatalf("无业务应答时不能标记线路可用：%+v %v", report, err)
	}
}
