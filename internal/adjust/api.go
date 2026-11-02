package adjust

import "quotes/internal/core"

// FactorKey returns the map key shared by gbbq lookup and download workers.
func FactorKey(market uint8, code string) string { return factorKey(market, code) }

func LoadGBBQ(path string) (map[string][]AdjustmentEvent, error) { return loadGBBQ(path) }

func WriteAdjustmentCSV(output string, security core.Security, events []AdjustmentEvent) error {
	return writeAdjustmentCSV(output, security, events)
}

func ReadAdjustmentCSV(path string) ([]AdjustmentEvent, error) { return readAdjustmentCSV(path) }

func AdjustBars(bars []core.Bar, events []AdjustmentEvent, mode string) ([]core.Bar, error) {
	return adjustBars(bars, events, mode)
}

func ReadBarsCSV(path string) ([]core.Bar, error) { return readBarsCSV(path) }
