package sitecfg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
)

var hostSections = []string{"HQHOST", "TRYHQHOST", "INFOHOST", "WTHOST", "DSHOST"}
var singleHosts = []string{"CHATHOST", "ICQHOST"}

// Build 返回（实际 GBK 配置字节、回读结果、构建错误），全程不访问用户配置文件。
// 流程：复制模板 → 清旧编号并填新站点 → 合并可变字段 → 固定顺序序列化 → GBK 回读自检。
// Parse 的结构性错误和 Result.Errors 中的业务错误都会阻止输出，Warnings 则随结果保留。
func Build(spec Spec) ([]byte, Result, error) {
	base, err := loadTemplate()
	if err != nil {
		return nil, Result{}, err
	}
	// 第 1 步：深复制模板，构建互不共享状态；必须至少有一个非空地址的主行情站。
	doc := base.clone()
	hosts := validHosts(spec.Hosts)
	if len(hosts) == 0 {
		return nil, Result{}, fmt.Errorf("spec.hosts 为空，至少要一台行情主站")
	}
	// 第 2 步：删除模板残留编号键，再重新连续编号填充各业务分区，不能留下旧地址或旧 HostNum。
	for _, sec := range doc.sections {
		for key := range sec.values {
			if numberedKey.MatchString(key) {
				sec.remove(key)
			}
		}
	}
	fillHosts(doc.sections["HQHOST"], "HQHOST", hosts, "行情主站", 7709, spec.Primary)
	fillHosts(doc.sections["TRYHQHOST"], "TRYHQHOST", validHosts(spec.TryHosts), "候选站", 7709, 0)
	fillHosts(doc.sections["DSHOST"], "DSHOST", validHosts(spec.DSHosts), "数据站", 7720, 0)
	fillHosts(doc.sections["INFOHOST"], "INFOHOST", validHosts(spec.InfoHosts), "资讯站", 7721, 0)
	fillHosts(doc.sections["WTHOST"], "WTHOST", validHosts(spec.WTHosts), "交易站", 7711, 0)

	// 第 3 步：合并单站、主站引用、普通分区和身份字段；map 键排序保证同输入输出可重复。
	for _, name := range sortedHostKeys(spec.Single) {
		if !contains(singleHosts, name) {
			continue
		}
		host := spec.Single[name]
		if strings.TrimSpace(host.IP) == "" {
			continue
		}
		port := host.Port
		if port == 0 {
			port = 7710
		}
		sec := doc.ensureSection(name)
		sec.set("IPAddress", strings.TrimSpace(host.IP))
		sec.set("Port", strconv.Itoa(port))
	}
	for _, key := range sortedIntKeys(spec.OtherHost) {
		doc.ensureSection("OTHERHOST").set(key, strconv.Itoa(spec.OtherHost[key]))
	}
	for _, name := range sortedSectionKeys(spec.Sections) {
		if contains(hostSections, name) {
			continue
		}
		sec := doc.ensureSection(name)
		values := spec.Sections[name]
		for _, key := range sortedStringKeys(values) {
			sec.set(key, values[key])
		}
	}
	for _, key := range sortedStringKeys(spec.Identity) {
		target := "UserHost"
		if _, ok := doc.sections["Other"].values[key]; ok {
			target = "Other"
		} else if _, ok := doc.sections["user"].values[key]; ok {
			target = "user"
		}
		doc.ensureSection(target).set(key, spec.Identity[key])
	}

	// 第 4 步：按模板分区及键顺序生成 CRLF 文本，再转成客户端实际使用的 GBK 字节。
	var text strings.Builder
	for _, name := range doc.order {
		fmt.Fprintf(&text, "[%s]\r\n", name)
		doc.sections[name].text(&text)
		text.WriteString("\r\n")
	}
	// 与原始 Python join 写法一致：末项后只保留一份 CRLF，不额外多一个空白行。
	encodedText := strings.TrimSuffix(text.String(), "\r\n")
	raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(encodedText))
	if err != nil {
		return nil, Result{}, fmt.Errorf("站点配置 GBK 编码失败：%w", err)
	}
	// 第 5 步：用真实输出字节重新解码和校验，而非只检查尚未编码的内存结构。
	result, err := Parse(raw)
	if err != nil {
		return nil, Result{}, fmt.Errorf("生成结果回读失败：%w", err)
	}
	if len(result.Errors) > 0 {
		return nil, result, fmt.Errorf("生成结果自检失败：%s", strings.Join(result.Errors[:min(6, len(result.Errors))], "；"))
	}
	return raw, result, nil
}

// Save 先构建并通过回读校验，再用同目录临时文件替换目标；无效配置不能覆盖原文件。
func Save(path string, spec Spec) (Result, error) {
	raw, result, err := Build(spec)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return result, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".connect-*.tmp")
	if err != nil {
		return result, err
	}
	temp := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(temp)
		}
	}()
	if _, err = file.Write(raw); err == nil {
		err = file.Close()
	}
	if err == nil {
		err = os.Rename(temp, path)
	}
	if err != nil {
		return result, err
	}
	ok = true
	return result, nil
}

func validHosts(input []Host) []Host {
	out := make([]Host, 0, len(input))
	for _, host := range input {
		if strings.TrimSpace(host.IP) != "" {
			out = append(out, host)
		}
	}
	return out
}

func fillHosts(sec *section, name string, hosts []Host, defaultName string, defaultPort, primary int) {
	for key := range sec.values {
		for _, prefix := range hostFields {
			if strings.HasPrefix(key, prefix) {
				sec.remove(key)
				break
			}
		}
	}
	sec.set("HostNum", strconv.Itoa(len(hosts)))
	if primary < 0 || primary >= len(hosts) {
		primary = 0
	}
	sec.set("PrimaryHost", strconv.Itoa(primary))
	for i, host := range hosts {
		tag := fmt.Sprintf("%02d", i+1)
		hostName := host.Name
		if hostName == "" {
			hostName = defaultName + strconv.Itoa(i+1)
		}
		port := host.Port
		if port == 0 {
			port = defaultPort
		}
		sec.set("HostName"+tag, hostName)
		sec.set("IPAddress"+tag, strings.TrimSpace(host.IP))
		sec.set("Port"+tag, strconv.Itoa(port))
		if name == "HQHOST" || name == "TRYHQHOST" || host.Weight != 0 {
			weight := host.Weight
			if weight == 0 {
				weight = 90
			}
			sec.set("Weight"+tag, strconv.Itoa(weight))
		}
		if host.Areas != "" || name == "HQHOST" || name == "TRYHQHOST" || name == "INFOHOST" || name == "DSHOST" {
			sec.set("Areas"+tag, host.Areas)
		}
		if host.HostType != nil {
			sec.set("HostType"+tag, strconv.Itoa(*host.HostType))
		}
	}
}

func (d *document) ensureSection(name string) *section {
	if sec := d.sections[name]; sec != nil {
		return sec
	}
	sec := &section{values: make(map[string]string)}
	d.sections[name] = sec
	// 未知分区只保存在索引中，不加入输出顺序，与参考实现固定分区写法保持一致。
	return sec
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortedHostKeys(values map[string]Host) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func sortedIntKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func sortedSectionKeys(values map[string]map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
