package sitecfg

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// ParsedHost 是配置回读后规范化的单条服务器记录，保留所属分区以便选线过滤。
type ParsedHost struct {
	Section string `json:"section"` // 记录所属的配置分区，如 HQHOST 或 DSHOST。
	Index   int    `json:"index"`   // 配置编号，从 1 开始；不同于 Spec.Primary 使用的零基下标。
	Name    string `json:"name"`    // 解析到的站点名，单站分区可能为空。
	IP      string `json:"ip"`      // 去除首尾空白后的 IP/域名，非法值同时记录到 Result.Errors。
	Port    int    `json:"port"`    // 解析后的 TCP 端口；需结合 Result.Errors 判断是否通过校验。
	Weight  int    `json:"weight"`  // 解析后的权重，批量站点缺省文本按 90 处理。
	Areas   string `json:"areas"`   // 配置中的区域字段原值。
}

// Result 同时保留可解析的数据和业务诊断；Parse 返回 nil 错误不意味着 Errors 为空。
type Result struct {
	Order    []string                     `json:"order"`    // 输入中出现的分区顺序，用于诊断重复/缺失分区。
	Sections map[string]map[string]string `json:"sections"` // 分区名→键值表，保留未转成 Host 的配置字段。
	Hosts    []ParsedHost                 `json:"hosts"`    // 所有已解析站点，可能来自不同业务分区；不能未经筛选全部当作行情线路。
	Errors   []string                     `json:"errors"`   // 致命业务校验错误；Build 发现非空会拒绝输出配置。
	Warnings []string                     `json:"warns"`    // 可继续使用的提示，如跨分区重复地址、缺展示名等。
}

var domainPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
var trailingIndex = regexp.MustCompile(`([0-9]{2})$`)

// Parse 从 GBK 字节回读配置，流程为：解码 → 建分区索引 → 校验分区 → 校验批量站点 → 校验单站/引用。
// error 只表示无法解码/载入模板；业务不合法会记入 Result.Errors，调用方必须另行检查。
// 缺失、未知、重复分区以及段内重复地址为致命问题；跨段重复等情况只记警告。
func Parse(raw []byte) (Result, error) {
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
	if err != nil {
		return Result{}, fmt.Errorf("GBK 解码失败：%w", err)
	}
	template, err := loadTemplate()
	if err != nil {
		return Result{}, err
	}
	// 第 1 步：逐行识别分区与键值，保留行号诊断；不先把未知格式默默丢掉。
	result := Result{Sections: make(map[string]map[string]string)}
	current := ""
	for no, line := range strings.Split(string(decoded), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = line[1 : len(line)-1]
			if _, exists := result.Sections[current]; exists {
				result.Errors = append(result.Errors, fmt.Sprintf("L%d 重复分区 [%s]", no+1, current))
			}
			result.Order = append(result.Order, current)
			result.Sections[current] = make(map[string]string)
			continue
		}
		if current == "" {
			if line != "" {
				result.Errors = append(result.Errors, fmt.Sprintf("L%d 分区外的行: %s", no+1, truncate(line, 40)))
			}
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			result.Sections[current][key] = value
		} else if line != "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("L%d 空行/无法解析: %s", no+1, truncate(line, 40)))
		}
	}
	// 第 2 步：与模板的固定分区集合比对；缺分区或新出现未知分区都记录业务错误。
	for _, name := range template.order {
		if _, ok := result.Sections[name]; !ok {
			result.Errors = append(result.Errors, fmt.Sprintf("缺分区 [%s]", name))
		}
	}
	for name := range result.Sections {
		if !contains(template.order, name) {
			result.Errors = append(result.Errors, fmt.Sprintf("未知分区 [%s]", name))
		}
	}

	// 第 3 步：解析每组编号站点，检查 HostNum、连续编号、地址、端口及跨/段内重复。
	seen := make(map[string]string) // 地址+端口→首次出现的 分区/序号，用于区分段内错误与跨段警告。
	for _, name := range hostSections {
		kv := result.Sections[name]
		indices := numberedIndices(kv)
		n := 0
		rawCount := strings.TrimSpace(kv["HostNum"])
		if isDigits(rawCount) {
			n, _ = strconv.Atoi(rawCount)
			if len(indices) > 0 && !continuous(indices) {
				residual := make([]int, 0)
				for _, index := range indices {
					if index > n {
						residual = append(residual, index)
					}
				}
				if len(residual) > 8 {
					residual = residual[:8]
				}
				result.Errors = append(result.Errors, fmt.Sprintf("[%s] 编号不连续，残留 %v", name, residual))
			}
			if n != len(indices) {
				result.Errors = append(result.Errors, fmt.Sprintf("[%s] HostNum=%d 但编号键有 %d 组", name, n, len(indices)))
			}
		} else if len(indices) > 0 {
			n = indices[len(indices)-1]
			result.Warnings = append(result.Warnings, fmt.Sprintf("[%s] 缺 HostNum，按最大编号推断为 %d", name, n))
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf("[%s] 空段", name))
		}
		for i := 1; i <= n; i++ {
			tag := fmt.Sprintf("%02d", i)
			ip := strings.TrimSpace(kv["IPAddress"+tag])
			hostName := strings.TrimSpace(kv["HostName"+tag])
			portText := strings.TrimSpace(kv["Port"+tag])
			weightText, hasWeight := kv["Weight"+tag]
			if !hasWeight {
				weightText = "90"
			}
			weightText = strings.TrimSpace(weightText)
			if !validAddress(ip) {
				result.Errors = append(result.Errors, fmt.Sprintf("[%s] %s 地址非法: %q", name, tag, ip))
			}
			if hostName == "" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("[%s] %s 缺名称", name, tag))
			}
			port, portErr := strconv.Atoi(portText)
			if !isDigits(portText) || portErr != nil || port < 1 || port > 65535 {
				result.Errors = append(result.Errors, fmt.Sprintf("[%s] %s 端口非法: %q", name, tag, portText))
			}
			weight, weightErr := strconv.Atoi(weightText)
			if weightErr != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("[%s] %s 权重非法: %q", name, tag, weightText))
			}
			address := ip + "\x00" + portText
			if previous := seen[address]; previous != "" && ip != "" {
				if strings.HasPrefix(previous, name+"/") {
					result.Errors = append(result.Errors, fmt.Sprintf("[%s] %s 段内重复 (%s:%s)", name, tag, ip, portText))
				} else {
					result.Warnings = append(result.Warnings, fmt.Sprintf("[%s] %s 与 %s 重复 (%s:%s)", name, tag, previous, ip, portText))
				}
			}
			if ip != "" && seen[address] == "" {
				seen[address] = name + "/" + tag
			}
			result.Hosts = append(result.Hosts, ParsedHost{Section: name, Index: i, Name: hostName, IP: ip, Port: port, Weight: weight, Areas: kv["Areas"+tag]})
		}
		primary := kv["PrimaryHost"]
		if value, err := strconv.Atoi(strings.TrimSpace(primary)); err == nil && n > 0 && value > n {
			result.Errors = append(result.Errors, fmt.Sprintf("[%s] PrimaryHost=%s 超出 HostNum=%d", name, primary, n))
		}
	}
	// 第 4 步：单站分区没有 NN 后缀，按无编号的 IPAddress/Port 读取，不能套用批量编号规则。
	for _, name := range singleHosts {
		kv := result.Sections[name]
		ip := strings.TrimSpace(kv["IPAddress"])
		portText := strings.TrimSpace(kv["Port"])
		if ip != "" && !validAddress(ip) {
			result.Errors = append(result.Errors, fmt.Sprintf("[%s] 地址非法: %q", name, ip))
		}
		port, portErr := strconv.Atoi(portText)
		if portText != "" && (!isDigits(portText) || portErr != nil || port < 1 || port > 65535) {
			result.Errors = append(result.Errors, fmt.Sprintf("[%s] 端口非法: %q", name, portText))
		}
		if ip != "" {
			result.Hosts = append(result.Hosts, ParsedHost{Section: name, Index: 1, IP: ip, Port: port})
		}
	}
	// 第 5 步：校验 OTHERHOST 的引用值；保留负数禁用语义，明显超大引用只给兼容性提示。
	for _, key := range otherHostKeys {
		value, ok := result.Sections["OTHERHOST"][key]
		if !ok {
			result.Warnings = append(result.Warnings, "[OTHERHOST] 缺 "+key)
		} else if number, err := strconv.Atoi(strings.TrimSpace(value)); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("[OTHERHOST] %s 非整数: %q", key, value))
		} else if number > 60 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("[OTHERHOST] %s=%s 引用可能越界", key, value))
		}
	}
	return result, nil
}

func validAddress(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if ip := net.ParseIP(value); ip != nil {
		return true
	}
	return domainPattern.MatchString(value)
}

func numberedIndices(kv map[string]string) []int {
	unique := make(map[int]bool)
	for key := range kv {
		match := trailingIndex.FindStringSubmatch(key)
		if match != nil {
			index, _ := strconv.Atoi(match[1])
			unique[index] = true
		}
	}
	out := make([]int, 0, len(unique))
	for index := range unique {
		out = append(out, index)
	}
	sort.Ints(out)
	return out
}

func continuous(indices []int) bool {
	for i, index := range indices {
		if index != i+1 {
			return false
		}
	}
	return true
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
