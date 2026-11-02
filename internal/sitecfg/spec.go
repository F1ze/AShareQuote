// Package sitecfg builds and validates TongdaXin connect.cfg documents.
// The default workflow is entirely in memory and needs no Python or reference
// installation. Invalid builds are rejected before any bytes can be persisted.
package sitecfg

// Host describes one numbered host entry in a configuration section.
type Host struct {
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Weight   int    `json:"weight,omitempty"`
	Areas    string `json:"areas,omitempty"`
	HostType *int   `json:"hosttype,omitempty"`
}

// Spec contains only the variable part of a site configuration. Stable fields
// come from the embedded template.
type Spec struct {
	Identity  map[string]string            `json:"identity"`
	Hosts     []Host                       `json:"hosts"`
	TryHosts  []Host                       `json:"try_hosts"`
	DSHosts   []Host                       `json:"ds_hosts"`
	InfoHosts []Host                       `json:"info_hosts"`
	WTHosts   []Host                       `json:"wt_hosts"`
	Single    map[string]Host              `json:"single"`
	OtherHost map[string]int               `json:"otherhost"`
	Primary   int                          `json:"primary"`
	Sections  map[string]map[string]string `json:"sections"`
}

var otherHostKeys = []string{
	"L2PrimaryHost", "SSDSPrimaryHost", "SSDS2PrimaryHost", "SSDS3PrimaryHost",
	"VipHQPrimaryHost", "VipHQ2PrimaryHost",
}

type endpoint struct {
	ip   string
	port int
}

// SeedSpec returns a fresh, zero-external-dependency configuration spec.
func SeedSpec() Spec {
	hosts := func(rows []endpoint, weight int) []Host {
		out := make([]Host, 0, len(rows))
		for _, row := range rows {
			out = append(out, Host{Name: "站", IP: row.ip, Port: row.port, Weight: weight})
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
			{"123.125.108.101", 7709}, {"114.141.177.118", 7709},
			{"27.151.2.90", 7709}, {"202.100.166.12", 7709},
			{"114.117.72.207", 7709}, {"182.118.8.9", 7709},
		}, 90),
		TryHosts: hosts([]endpoint{
			{"114.117.72.207", 7709}, {"114.141.177.118", 7709},
			{"27.151.2.90", 7709}, {"202.100.166.12", 7709},
			{"183.201.231.85", 7709},
		}, 90),
		DSHosts: hosts([]endpoint{
			{"114.117.72.207", 7720}, {"118.31.28.30", 7730}, {"114.141.177.118", 7720},
		}, 0),
		InfoHosts: hosts([]endpoint{
			{"114.117.72.207", 7711}, {"1.202.143.37", 7711},
			{"202.100.166.88", 7711}, {"114.141.177.75", 7711},
		}, 0),
		WTHosts: hosts([]endpoint{
			{"61.135.173.138", 7708}, {"211.100.23.198", 7708}, {"61.151.252.170", 7708},
		}, 0),
		Single:    map[string]Host{},
		OtherHost: other,
		Primary:   0,
		Sections: map[string]map[string]string{
			"UserHost": {"IPAddress": "114.117.72.207", "Port": "7715"},
		},
	}
}
