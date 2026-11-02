package sitecfg

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed template.json
var templateJSON []byte

type templateEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type templateSection struct {
	Name    string          `json:"name"`
	Entries []templateEntry `json:"entries"`
}

type document struct {
	order    []string
	sections map[string]*section
}

type section struct {
	entries []templateEntry
	values  map[string]string
}

var numberedKey = regexp.MustCompile(`.*[0-9]{2}$`)
var hostFields = []string{"HostName", "IPAddress", "Port", "Weight", "Areas", "HostType"}

func loadTemplate() (*document, error) {
	var payload struct {
		Sections []templateSection `json:"sections"`
	}
	if err := json.Unmarshal(templateJSON, &payload); err != nil {
		return nil, fmt.Errorf("内置站点模板无效：%w", err)
	}
	doc := &document{sections: make(map[string]*section, len(payload.Sections))}
	for _, item := range payload.Sections {
		sec := &section{entries: append([]templateEntry(nil), item.Entries...), values: make(map[string]string, len(item.Entries))}
		for _, entry := range item.Entries {
			sec.values[entry.Key] = entry.Value
		}
		doc.order = append(doc.order, item.Name)
		doc.sections[item.Name] = sec
	}
	return doc, nil
}

func (d *document) clone() *document {
	out := &document{order: append([]string(nil), d.order...), sections: make(map[string]*section, len(d.sections))}
	for name, sec := range d.sections {
		copied := &section{entries: append([]templateEntry(nil), sec.entries...), values: make(map[string]string, len(sec.values))}
		for key, value := range sec.values {
			copied.values[key] = value
		}
		out.sections[name] = copied
	}
	return out
}

func (s *section) ensure() {
	if s.values == nil {
		s.values = make(map[string]string)
	}
}

func (s *section) set(key, value string) {
	s.ensure()
	if _, exists := s.values[key]; !exists {
		s.entries = append(s.entries, templateEntry{Key: key})
	}
	s.values[key] = value
	for i := range s.entries {
		if s.entries[i].Key == key {
			s.entries[i].Value = value
			return
		}
	}
}

func (s *section) remove(key string) {
	delete(s.values, key)
	for i, entry := range s.entries {
		if entry.Key == key {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

func (s *section) text(builder *strings.Builder) {
	for _, entry := range s.entries {
		if value, ok := s.values[entry.Key]; ok {
			fmt.Fprintf(builder, "%s=%s\r\n", entry.Key, value)
		}
	}
}
