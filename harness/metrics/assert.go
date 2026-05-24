package metrics

import (
	"math"
	"strings"
	"testing"
	"time"
)

// AssertNear fails t if |got-expected|/max(expected,1) > tol.
// Mirrors the bash assert_near helper in scenarios/lib/common.sh.
func AssertNear(t *testing.T, label string, got, expected, tol float64) {
	t.Helper()
	if expected <= 0 {
		if got == 0 {
			t.Logf("PASS  %s: got 0 (expected 0)", label)
			return
		}
		t.Errorf("FAIL  %s: got %.0f (expected 0)", label, got)
		return
	}
	diff := math.Abs(got - expected)
	limit := expected * tol
	if diff <= limit {
		t.Logf("PASS  %s: got %.0f expected~%.0f (tol=%.2f, diff=%.0f <= %.0f)",
			label, got, expected, tol, diff, limit)
	} else {
		t.Errorf("FAIL  %s: got %.0f expected~%.0f (tol=%.2f, diff=%.0f > %.0f)",
			label, got, expected, tol, diff, limit)
	}
}

// AssertGTE fails t if got < min.
func AssertGTE(t *testing.T, label string, got, min float64) {
	t.Helper()
	if got >= min {
		t.Logf("PASS  %s: got %.0f >= %.0f", label, got, min)
	} else {
		t.Errorf("FAIL  %s: got %.0f < %.0f", label, got, min)
	}
}

// AssertGT fails t if got <= 0.
func AssertGT(t *testing.T, label string, got float64) {
	t.Helper()
	if got > 0 {
		t.Logf("PASS  %s: got %.0f > 0", label, got)
	} else {
		t.Errorf("FAIL  %s: got 0 or negative (expected > 0)", label)
	}
}

// AssertZero fails t if got != 0.
func AssertZero(t *testing.T, label string, got float64) {
	t.Helper()
	if got == 0 {
		t.Logf("PASS  %s: got 0", label)
	} else {
		t.Errorf("FAIL  %s: got %.0f (expected 0)", label, got)
	}
}

// AssertLT fails t if got >= max.
func AssertLT(t *testing.T, label string, got, max float64) {
	t.Helper()
	if got < max {
		t.Logf("PASS  %s: got %.0f < %.0f", label, got, max)
	} else {
		t.Errorf("FAIL  %s: got %.0f >= %.0f", label, got, max)
	}
}

// Delta returns after-before, handling Prometheus counter wraps (2^64).
func Delta(before, after float64) float64 {
	d := after - before
	if d < 0 {
		d += math.Pow(2, 64)
	}
	return d
}

// WaitForGTE polls url/metricName until its value >= min, or until timeout.
// Returns the final value.
func WaitForGTE(t *testing.T, url, metric string, min float64, timeout time.Duration) float64 {
	t.Helper()
	v, ok := WaitFor(url, metric, func(v float64) bool { return v >= min }, timeout, 500*time.Millisecond)
	if !ok {
		t.Logf("WaitForGTE %s: timed out waiting for %.0f; last=%.0f", metric, min, v)
	}
	return v
}

// ScrapeOrFail scrapes the URL and fails the test on error.
func ScrapeOrFail(t *testing.T, url string) map[string]float64 {
	t.Helper()
	m, err := Scrape(url)
	if err != nil {
		t.Fatalf("metrics scrape %s: %v", url, err)
	}
	return m
}

// LabelledValue returns the value of metricName{labelKey=labelVal} from a raw
// Prometheus text body already stored in m (from a prior Scrape call) by
// re-scraping the same URL filtered by label.
//
// Note: since Scrape aggregates all label combinations, use ScrapeWithLabel
// directly when per-label granularity is needed.
func LabelledValue(t *testing.T, url, metricName, labelKey, labelVal string) float64 {
	t.Helper()
	v, err := ScrapeWithLabel(url, metricName, labelKey, labelVal)
	if err != nil {
		t.Fatalf("ScrapeWithLabel %s{%s=%s}: %v", metricName, labelKey, labelVal, err)
	}
	return v
}

// Snapshot returns a snapshot of all metrics at url. Fails the test on error.
func Snapshot(t *testing.T, label, url string) map[string]float64 {
	t.Helper()
	m, err := Scrape(url)
	if err != nil {
		t.Fatalf("snapshot %s (%s): %v", label, url, err)
	}
	t.Logf("snapshot %s: %d metrics", label, len(m))
	return m
}

// DeltaMap returns a map of after[k]-before[k] for all keys present in after.
func DeltaMap(before, after map[string]float64) map[string]float64 {
	d := make(map[string]float64, len(after))
	for k, v := range after {
		d[k] = Delta(before[k], v)
	}
	return d
}

// Logf logs a human-readable dump of the delta metrics whose names contain
// any of the given substrings.
func Logf(t *testing.T, prefix string, delta map[string]float64, contains ...string) {
	t.Helper()
	for k, v := range delta {
		for _, s := range contains {
			if contains_(k, s) {
				t.Logf("%s  %s = %.0f", prefix, k, v)
				break
			}
		}
	}
}

func contains_(s, sub string) bool {
	return len(sub) > 0 && strings.Contains(s, sub)
}
