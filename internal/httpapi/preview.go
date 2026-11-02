package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"quotes/internal/adjust"
)

type PreviewBar struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
	Amount float64 `json:"amount"`
}

type PreviewResponse struct {
	File            string       `json:"file"`
	Mode            string       `json:"mode"`
	Total           int          `json:"total"`
	FactorAvailable bool         `json:"factorAvailable"`
	FactorEvents    int          `json:"factorEvents"`
	Bars            []PreviewBar `json:"bars"`
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".csv") || strings.HasSuffix(name, "_gbbq.csv") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("预览文件名无效"))
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "raw"
	}
	limit := 200
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 800 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("预览条数须为 1–800"))
			return
		}
		limit = parsed
	}
	output := s.manager.OutputDir()
	bars, err := adjust.ReadBarsCSV(filepath.Join(output, name))
	if err != nil {
		status := http.StatusInternalServerError
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, fmt.Errorf("读取行情预览：%w", err))
		return
	}
	stem := strings.SplitN(name, "_", 2)[0]
	factorPath := filepath.Join(output, stem+"_gbbq.csv")
	_, factorStatErr := os.Stat(factorPath)
	factorAvailable := factorStatErr == nil
	events := []adjust.AdjustmentEvent(nil)
	if mode != "raw" {
		if !factorAvailable {
			writeError(w, http.StatusNotFound, fmt.Errorf("未找到该证券的复权因子文件，请确认下载时已配置 gbbq"))
			return
		}
		events, err = adjust.ReadAdjustmentCSV(factorPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("读取复权因子：%w", err))
			return
		}
	}
	adjusted, err := adjust.AdjustBars(bars, events, mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	total := len(adjusted)
	if len(adjusted) > limit {
		adjusted = adjusted[len(adjusted)-limit:]
	}
	response := PreviewResponse{File: name, Mode: mode, Total: total, FactorAvailable: factorAvailable, FactorEvents: len(events), Bars: make([]PreviewBar, 0, len(adjusted))}
	for _, bar := range adjusted {
		layout := "2006-01-02"
		if bar.Date.Hour() != 0 || bar.Date.Minute() != 0 {
			layout = "2006-01-02 15:04"
		}
		response.Bars = append(response.Bars, PreviewBar{Date: bar.Date.Format(layout), Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, Amount: bar.Amount})
	}
	writeJSON(w, http.StatusOK, response)
}
