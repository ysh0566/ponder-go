package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/ysh0566/ponder-go/internal/version"
)

var goVersionInfo = prometheus.MustNewConstMetric(
	prometheus.NewDesc("ponder_version_info", "Ponder version information", []string{"version", "major", "minor", "patch"}, nil),
	prometheus.GaugeValue, 1, version.Full, version.Major, version.Minor, version.Patch,
)

type applicationInfo struct {
	version, settings prometheus.Metric
}

// An unchecked collector allows the standalone reader to omit the ordering label.
func (*applicationInfo) Describe(chan<- *prometheus.Desc) {}

func (i *applicationInfo) Collect(out chan<- prometheus.Metric) {
	// Gather holds Registry.mu until all collectors finish.
	if i.version != nil {
		out <- i.version
	}
	if i.settings != nil {
		out <- i.settings
	}
}

// SetSettings publishes the Go version and replaces the active command's settings.
func (r *Registry) SetSettings(command, database, ordering string) {
	labels := prometheus.Labels{"command": command, "database": database}
	if command != "serve" {
		labels["ordering"] = ordering
	}
	desc := prometheus.NewDesc("ponder_settings_info", "Ponder settings information", nil, labels)
	sample := prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, 1)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.info = applicationInfo{version: goVersionInfo, settings: sample}
}
