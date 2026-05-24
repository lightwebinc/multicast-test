// Package metrics provides helpers for scraping and asserting Prometheus
// metrics from test nodes.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Scrape fetches the /metrics endpoint at url and returns a map of
// metricName → summed value across all label combinations.
// Counter families are returned as their current value (not a delta).
func Scrape(url string) (map[string]float64, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("scrape %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body %s: %w", url, err)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	if err != nil && len(families) == 0 {
		return nil, fmt.Errorf("parse metrics %s: %w", url, err)
	}

	result := make(map[string]float64)
	for name, mf := range families {
		var sum float64
		for _, m := range mf.GetMetric() {
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				if c := m.GetCounter(); c != nil {
					sum += c.GetValue()
				}
			case dto.MetricType_GAUGE:
				if g := m.GetGauge(); g != nil {
					sum += g.GetValue()
				}
			}
		}
		result[name] = sum
	}
	return result, nil
}

// ScrapeWithLabel fetches metrics and returns the summed value of samples whose
// label set contains the key=value pair.
func ScrapeWithLabel(url, metricName, labelKey, labelVal string) (float64, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return 0, fmt.Errorf("scrape %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, _ := parser.TextToMetricFamilies(strings.NewReader(string(body)))

	mf, ok := families[metricName]
	if !ok {
		return 0, nil
	}

	var sum float64
	for _, m := range mf.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetName() == labelKey && lp.GetValue() == labelVal {
				switch mf.GetType() {
				case dto.MetricType_COUNTER:
					if c := m.GetCounter(); c != nil {
						sum += c.GetValue()
					}
				case dto.MetricType_GAUGE:
					if g := m.GetGauge(); g != nil {
						sum += g.GetValue()
					}
				}
				break
			}
		}
	}
	return sum, nil
}

// WaitFor polls url every interval until the named metric satisfies pred, or
// until timeout expires. Returns the final value and whether pred was satisfied.
func WaitFor(url, metricName string, pred func(float64) bool, timeout, interval time.Duration) (float64, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m, err := Scrape(url)
		if err == nil {
			if v, ok := m[metricName]; ok && pred(v) {
				return v, true
			}
		}
		time.Sleep(interval)
	}
	m, _ := Scrape(url)
	return m[metricName], false
}
