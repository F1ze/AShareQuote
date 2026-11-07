package domain

import "testing"

func TestAShareExchangeClassification(t *testing.T) {
	for _, group := range []struct {
		name     string
		market   uint8
		accepted []string
		rejected []string
	}{
		{"深圳", 0,
			[]string{"000001", "000999", "001000", "001200", "001999", "002001", "003816", "004000", "004999", "300000", "301001", "302132", "308999", "309799"},
			[]string{"000000", "001001", "001199", "005000", "030001", "070001", "080001", "159915", "200001", "299999", "309800", "309999", "310000", "399001", "600000", "920001"}},
		{"上海", 1,
			[]string{"600000", "600999", "601000", "601999", "603000", "603999", "605000", "605999", "688000", "688999"},
			[]string{"000001", "110001", "510300", "588000", "599999", "602000", "602999", "604000", "604999", "606000", "687999", "689000", "689009", "730000", "787001", "900001"}},
		{"北京", 2,
			[]string{"430000", "430047", "439999", "830000", "830001", "839999", "870000", "879999", "880000", "888999", "920000", "920001", "920999"},
			[]string{"400001", "420001", "429999", "440000", "820001", "829999", "840000", "869999", "889000", "889001", "889999", "890000", "899050", "919999", "921000", "600000"}},
	} {
		t.Run(group.name, func(t *testing.T) {
			for _, code := range group.accepted {
				t.Run(code, func(t *testing.T) {
					if !IsAShare(Security{Market: group.market, Code: code}) {
						t.Fatal("普通股候选号段被排除")
					}
				})
			}
			for _, code := range group.rejected {
				t.Run(code, func(t *testing.T) {
					if IsAShare(Security{Market: group.market, Code: code}) {
						t.Fatal("非普通股、错误市场或号段外代码被纳入")
					}
				})
			}
		})
	}
}

func TestAShareRejectsMalformedCodesAndUnknownMarkets(t *testing.T) {
	for _, code := range []string{"", "60000", "6000000", "60000x", "６０００００", "六000", " 60000", "60000 ", "+00001", "-00001", "000.01", "00\x00001", "sh600000"} {
		for _, market := range []uint8{0, 1, 2} {
			if IsAShare(Security{Market: market, Code: code}) {
				t.Errorf("接受非法代码：market=%d code=%q", market, code)
			}
		}
	}
	for _, market := range []uint8{3, 255} {
		for _, code := range []string{"000001", "600000", "920001"} {
			if IsAShare(Security{Market: market, Code: code}) {
				t.Errorf("接受未知市场：market=%d code=%q", market, code)
			}
		}
	}
}

// 历史采集不能按当前名称或上市状态丢掉 ST/退市样本；号段分类也不负责公司新旧代码映射。
func TestAShareClassificationDoesNotDependOnCurrentMetadata(t *testing.T) {
	for _, security := range []Security{
		{Market: 0, Code: "302132", Name: "中航成飞"},
		{Market: 1, Code: "600001", Name: "退市样本", Status: "delisted", DelistingDate: "2019-01-01"},
		{Market: 2, Code: "430047", Name: "ST 历史样本", Status: "unknown"},
	} {
		if !IsAShare(security) {
			t.Errorf("名称或当前状态改变号段判断：%+v", security)
		}
	}
}
