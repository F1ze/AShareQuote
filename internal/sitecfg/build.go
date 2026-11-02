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

// Build constructs a complete GBK/CRLF configuration and parses it back in
// memory. A configuration with validation errors is never returned.
func Build(spec Spec) ([]byte, Result, error) {
	base, err := loadTemplate()
	if err != nil {
		return nil, Result{}, err
	}
	doc := base.clone()
	hosts := validHosts(spec.Hosts)
	if len(hosts) == 0 {
		return nil, Result{}, fmt.Errorf("spec.hosts 为空，至少要一台行情主站")
	}
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

	var text strings.Builder
	for _, name := range doc.order {
		fmt.Fprintf(&text, "[%s]\r\n", name)
		doc.sections[name].text(&text)
		text.WriteString("\r\n")
	}
	// Python's join-based writer leaves exactly one CRLF after the final entry.
	encodedText := strings.TrimSuffix(text.String(), "\r\n")
	raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(encodedText))
	if err != nil {
		return nil, Result{}, fmt.Errorf("站点配置 GBK 编码失败：%w", err)
	}
	result, err := Parse(raw)
	if err != nil {
		return nil, Result{}, fmt.Errorf("生成结果回读失败：%w", err)
	}
	if len(result.Errors) > 0 {
		return nil, result, fmt.Errorf("生成结果自检失败：%s", strings.Join(result.Errors[:min(6, len(result.Errors))], "；"))
	}
	return raw, result, nil
}

// Save builds and validates before creating or replacing the destination.
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
	// Unknown custom sections are intentionally omitted from serialization,
	// matching the reference implementation's fixed SITECFG_ORDER writer.
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
