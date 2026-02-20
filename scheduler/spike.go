/*
Spike Detection Module
=====================
Monitors Prometheus for traffic spike indicators and triggers
NEXUS activation. Implements Algorithm 1: Traffic Spike Detection.

Required by Section 2A of the professional review:
"It must trigger only when:
  - RPS threshold exceeded OR
  - HPA scaling event observed OR
  - p95 latency crosses defined bound"

The spike detector is the GATEKEEPER for the entire NEXUS system.
Without a spike event, NEXUS remains completely dormant.
*/

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"k8s.io/klog/v2"
)

// SpikeDetector monitors for traffic spikes using Prometheus metrics
type SpikeDetector struct {
	prometheusURL       string
	qpsThreshold        float64
	errorThreshold      float64
	p95LatencyThreshold float64 // milliseconds
	fallbackThreshold   int
	client              *http.Client
}

// PrometheusResponse represents the response from Prometheus API
type PrometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// NewSpikeDetector creates a new spike detector with configurable thresholds
func NewSpikeDetector() *SpikeDetector {
	prometheusURL := os.Getenv("PROMETHEUS_URL")
	if prometheusURL == "" {
		prometheusURL = "http://prometheus-server.monitoring:80"
	}

	qpsThreshold := 1000.0
	if qpsStr := os.Getenv("SPIKE_QPS_THRESHOLD"); qpsStr != "" {
		if val, err := strconv.ParseFloat(qpsStr, 64); err == nil {
			qpsThreshold = val
		}
	}

	errorThreshold := 50.0
	if errStr := os.Getenv("SPIKE_ERROR_THRESHOLD"); errStr != "" {
		if val, err := strconv.ParseFloat(errStr, 64); err == nil {
			errorThreshold = val
		}
	}

	p95LatencyThreshold := 500.0 // 500ms default
	if latStr := os.Getenv("SPIKE_P95_LATENCY_THRESHOLD"); latStr != "" {
		if val, err := strconv.ParseFloat(latStr, 64); err == nil {
			p95LatencyThreshold = val
		}
	}

	klog.Infof("Spike detector thresholds: QPS=%.0f, ErrorRate=%.0f, p95Latency=%.0fms",
		qpsThreshold, errorThreshold, p95LatencyThreshold)

	return &SpikeDetector{
		prometheusURL:       prometheusURL,
		qpsThreshold:        qpsThreshold,
		errorThreshold:      errorThreshold,
		p95LatencyThreshold: p95LatencyThreshold,
		fallbackThreshold:   5,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Detect checks if a spike is currently happening
// Implements Algorithm 1: Traffic Spike Detection
// Returns true if ANY spike indicator exceeds its threshold
func (sd *SpikeDetector) Detect(pendingPodCount int) bool {
	// Fallback: if Prometheus is unreachable, use pending pod count
	if !sd.isPrometheusReachable() {
		klog.V(2).Info("Prometheus unreachable, using fallback spike detection")
		return pendingPodCount >= sd.fallbackThreshold
	}

	// Check 1: QPS (Queries Per Second)
	qps, err := sd.queryQPS()
	if err != nil {
		klog.Warningf("Failed to query QPS: %v", err)
	} else if qps > sd.qpsThreshold {
		klog.Infof("SPIKE DETECTED: QPS %.2f > threshold %.2f", qps, sd.qpsThreshold)
		return true
	}

	// Check 2: Error Rate (5xx errors)
	errorRate, err := sd.queryErrorRate()
	if err != nil {
		klog.Warningf("Failed to query error rate: %v", err)
	} else if errorRate > sd.errorThreshold {
		klog.Infof("SPIKE DETECTED: Error rate %.2f > threshold %.2f", errorRate, sd.errorThreshold)
		return true
	}

	// Check 3: p95 Latency (professional requirement 2A)
	p95, err := sd.queryP95Latency()
	if err != nil {
		klog.Warningf("Failed to query p95 latency: %v", err)
	} else if p95 > sd.p95LatencyThreshold {
		klog.Infof("SPIKE DETECTED: p95 latency %.2fms > threshold %.2fms", p95, sd.p95LatencyThreshold)
		return true
	}

	// Check 4: HPA scale-up events
	hpaActive, err := sd.checkHPAActivity()
	if err != nil {
		klog.Warningf("Failed to check HPA activity: %v", err)
	} else if hpaActive {
		klog.Info("SPIKE DETECTED: HPA scale-up event detected")
		return true
	}

	// No spike detected
	klog.V(2).Infof("No spike detected (QPS: %.2f, ErrorRate: %.2f, p95: %.2fms)", qps, errorRate, p95)
	return false
}

// isPrometheusReachable checks if Prometheus is available
func (sd *SpikeDetector) isPrometheusReachable() bool {
	url := fmt.Sprintf("%s/api/v1/query?query=up", sd.prometheusURL)
	resp, err := sd.client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// queryQPS retrieves the current queries per second across all services
func (sd *SpikeDetector) queryQPS() (float64, error) {
	query := "sum(rate(http_server_request_count[1m]))"
	return sd.queryPrometheus(query)
}

// queryErrorRate retrieves the current 5xx error rate
func (sd *SpikeDetector) queryErrorRate() (float64, error) {
	query := `sum(rate(http_server_request_count{response_code=~"5.."}[1m]))`
	return sd.queryPrometheus(query)
}

// queryP95Latency retrieves the p95 request latency in milliseconds
func (sd *SpikeDetector) queryP95Latency() (float64, error) {
	query := `histogram_quantile(0.95, sum(rate(http_server_request_duration_seconds_bucket[1m])) by (le)) * 1000`
	return sd.queryPrometheus(query)
}

// checkHPAActivity checks if any HPA has recently scaled up
func (sd *SpikeDetector) checkHPAActivity() (bool, error) {
	query := "increase(kube_horizontalpodautoscaler_status_current_replicas[2m])"
	value, err := sd.queryPrometheus(query)
	if err != nil {
		return false, err
	}
	return value > 0, nil
}

// queryPrometheus executes a PromQL query and returns the numeric result
func (sd *SpikeDetector) queryPrometheus(query string) (float64, error) {
	url := fmt.Sprintf("%s/api/v1/query?query=%s", sd.prometheusURL, query)

	resp, err := sd.client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to query Prometheus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	var promResp PrometheusResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if promResp.Status != "success" {
		return 0, fmt.Errorf("prometheus query failed: %s", promResp.Status)
	}

	if len(promResp.Data.Result) == 0 {
		return 0, nil // No data
	}

	// Extract the numeric value
	if len(promResp.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("invalid result format")
	}

	valueStr, ok := promResp.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("value is not a string")
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse value: %w", err)
	}

	return value, nil
}
