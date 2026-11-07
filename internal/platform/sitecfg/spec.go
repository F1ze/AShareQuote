// Package sitecfg 生成并校验通达信 connect.cfg，默认全程在内存中完成，无需用户安装参考客户端。
// 先按模板构造，再按真实 GBK 字节回读验证；存在致命配置错误时不返回可保存的结果。
package sitecfg

// Host 是配置输入中的一台服务器；与 domain.Host 相比，额外保留配置生成所需的展示及模板字段。
type Host struct {
	Name     string `json:"name"`               // 写入 HostNameNN 的显示名，空值时由分区名称和序号生成。
	IP       string `json:"ip"`                 // 服务器 IP/域名；空值条目在构建前被过滤。
	Port     int    `json:"port"`               // TCP 端口，0 时采用所属分区默认端口。
	Weight   int    `json:"weight,omitempty"`   // 配置权重，行情分区未指定时写 90；不是探测耗时。
	Areas    string `json:"areas,omitempty"`    // 原样写入 AreasNN 的区域标记，允许空串。
	HostType *int   `json:"hosttype,omitempty"` // 可选 HostTypeNN；nil 表示不写，指向 0 表示显式写入零。
}

// Spec 仅描述相对于内置模板需要替换的部分；构建时复制模板，不修改原始模板数据。
type Spec struct {
	Identity  map[string]string            `json:"identity"`   // 身份/客户端键值覆盖，按模板现有键定位到 Other、user 或默认 UserHost。
	Hosts     []Host                       `json:"hosts"`      // HQHOST 主行情站列表，至少保留一个有效地址。
	TryHosts  []Host                       `json:"try_hosts"`  // TRYHQHOST 备用行情站列表，线路提供器与主站合并去重。
	DSHosts   []Host                       `json:"ds_hosts"`   // DSHOST 数据站列表，不混入 7709 行情 TCP 探测。
	InfoHosts []Host                       `json:"info_hosts"` // INFOHOST 资讯站列表。
	WTHosts   []Host                       `json:"wt_hosts"`   // WTHOST 交易站配置，仅生成配置，不执行交易。
	Single    map[string]Host              `json:"single"`     // 无编号单站分区，当前仅处理 CHATHOST/ICQHOST。
	OtherHost map[string]int               `json:"otherhost"`  // OTHERHOST 中各主站引用键的整数值，内置未启用引用使用 -1。
	Primary   int                          `json:"primary"`    // HQHOST 首选站的零基下标；负数或超出列表范围时归零。
	Sections  map[string]map[string]string `json:"sections"`   // 非批量站点分区的键值覆盖；未知分区不加入固定输出顺序。
}

var otherHostKeys = []string{
	"L2PrimaryHost", "SSDSPrimaryHost", "SSDS2PrimaryHost", "SSDS3PrimaryHost",
	"VipHQPrimaryHost", "VipHQ2PrimaryHost",
}

type endpoint struct {
	name string // 参考配置中的 HostName，用于配置和前端展示；不参与地址去重。
	ip   string // 内置种子站点地址，仅用于构造一份新的 Spec。
	port int    // 对应服务端口，区分行情、数据和资讯等业务。
}

// SeedSpec 每次返回独立的默认配置及映射，不读取用户 connect.cfg 或其他外部文件。
// 站点快照同步 original/connect.cfg：按实际编号保留全部条目，不能相信其中过时的 HostNum。
// 行情与备用站保留 IPv4/IPv6 候选；可用性由线路提供器逐条执行 TCP 探测决定。
func SeedSpec() Spec {
	hosts := func(rows []endpoint, weight int) []Host {
		out := make([]Host, 0, len(rows))
		for _, row := range rows {
			out = append(out, Host{Name: row.name, IP: row.ip, Port: row.port, Weight: weight})
		}
		return out
	}
	other := make(map[string]int, len(otherHostKeys))
	for _, key := range otherHostKeys {
		other[key] = -1
	}
	return Spec{
		Identity: map[string]string{},
		Hosts: hosts([]endpoint{
			{"银河证券腾讯云行情", "114.117.72.207", 7709},
			{"银河证券上证云北京", "123.125.108.101", 7709},
			{"银河证券上证云上海一", "114.141.177.118", 7709},
			{"银河证券上证云上海二", "114.141.177.40", 7709},
			{"银河证券上证云福州", "27.151.2.90", 7709},
			{"银河证券上证云新疆", "202.100.166.12", 7709},
			{"银河证券上证云郑州", "182.118.8.9", 7709},
			{"银河证券上证云太原", "183.201.231.85", 7709},
			{"银河证券IPV6行情", "2402:4e00:1600:1524:6:5989:fc84:a9b3", 7709},
			{"银河证券富丰电信", "1.202.143.37", 7709},
			{"银河证券富丰联通", "111.203.134.118", 7709},
			{"银河证券富丰移动", "117.133.128.226", 7709},
		}, 90),
		TryHosts: hosts([]endpoint{
			{"银河证券腾讯云行情", "114.117.72.207", 7709},
			{"银河证券IPV6行情", "2408:4003:1f20::1e6", 7709},
			{"银河证券上证云上海一", "114.141.177.118", 7709},
			{"银河证券上证云上海二", "114.141.177.40", 7709},
			{"银河证券上证云福州", "27.151.2.90", 7709},
			{"银河证券上证云新疆", "202.100.166.12", 7709},
			{"银河证券上证云太原", "183.201.231.85", 7709},
			{"银河证券上证云郑州", "182.118.8.9", 7709},
			{"银河证券上证云北京", "123.125.108.101", 7709},
			{"银河证券富丰电信", "1.202.143.37", 7709},
			{"银河证券富丰联通", "111.203.134.118", 7709},
			{"银河证券富丰移动", "117.133.128.226", 7709},
			{"银河证券IPV6行情", "2402:4e00:1600:1524:6:5989:fc84:a9b3", 7709},
		}, 90),
		DSHosts: hosts([]endpoint{
			{"银河腾讯云扩展行情", "114.117.72.207", 7720},
			{"银河IPV6扩展行情", "2402:4e00:1600:1524:6:5989:fc84:a9b3", 7720},
			{"银河阿里云扩展行情", "118.31.28.30", 7730},
		}, 0),
		// 福州资讯端口 771 按参考配置保留；非行情站不参与 7709 TCP 探测。
		InfoHosts: hosts([]endpoint{
			{"银河证券腾讯云资讯", "114.117.72.207", 7711},
			{"银河证券富丰电信", "1.202.143.37", 7711},
			{"银河证券富丰联通", "111.203.134.118", 7711},
			{"银河证券富丰移动", "117.133.128.226", 7711},
			{"银河证券IPV6资讯", "2402:4e00:1600:1524:6:5989:fc84:a9b3", 7711},
			{"银河证券上证云新疆", "202.100.166.88", 7711},
			{"银河证券上证云太原", "183.201.231.103", 7711},
			{"银河证券上证云上海", "114.141.177.75", 7711},
			{"银河证券上证云北京", "123.125.135.43", 7711},
			{"银河证券上证云福州", "27.151.2.171", 771},
		}, 0),
		WTHosts: hosts([]endpoint{
			{"银河证券北京网通", "61.135.173.138", 7708},
			{"银河证券北京电信", "211.100.23.198", 7708},
			{"银河证券上海", "61.151.252.170", 7708},
			{"银河证券武汉", "58.49.110.76", 7708},
			{"银河证券广州", "59.42.252.136", 7708},
			{"银河证券杭州电信", "218.75.75.18", 7708},
			{"银河证券杭州网通", "218.108.13.247", 7708},
			{"银河证券宁波信息港", "221.136.93.19", 7708},
			{"银河证券宁波网通", "221.12.53.53", 7708},
			{"银河证券宁波电信", "218.75.85.139", 7708},
		}, 0),
		Single:    map[string]Host{},
		OtherHost: other,
		Primary:   2,
		Sections: map[string]map[string]string{
			"UserHost": {"IPAddress": "114.117.72.207", "Port": "7715"},
		},
	}
}
