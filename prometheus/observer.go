package prometheus

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Observer bridges samsara supervisor telemetry into a Prometheus registry.
// It satisfies samsara.MetricsObserver structurally, so this package never
// imports samsara.
//
// All methods are non-blocking (in-memory metric updates only), matching the
// MetricsObserver contract that callbacks run on the supervisor goroutine.
//
// Exposed series:
//
//	samsara_component_up{component}                                  gauge
//	samsara_component_starts_total{component}                        counter
//	samsara_component_restarts_total{component}                      counter
//	samsara_component_stop_errors_total{component}                   counter
//	samsara_component_health_check_duration_seconds{component}       histogram
//	samsara_component_health_check_failures_total{component}         counter
type Observer struct {
	up            *prometheus.GaugeVec
	starts        *prometheus.CounterVec
	restarts      *prometheus.CounterVec
	stopErrors    *prometheus.CounterVec
	healthDur     *prometheus.HistogramVec
	healthFailure *prometheus.CounterVec
}

func newObserver(reg *prometheus.Registry) *Observer {
	o := &Observer{
		up: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "samsara_component_up",
			Help: "Whether a supervised component is currently running (1) or not (0).",
		}, []string{"component"}),
		starts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "samsara_component_starts_total",
			Help: "Total successful starts per supervised component, including restarts.",
		}, []string{"component"}),
		restarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "samsara_component_restarts_total",
			Help: "Total restart decisions per supervised component.",
		}, []string{"component"}),
		stopErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "samsara_component_stop_errors_total",
			Help: "Total Stop calls that returned an error, per supervised component.",
		}, []string{"component"}),
		healthDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "samsara_component_health_check_duration_seconds",
			Help:    "Health-check duration per supervised component.",
			Buckets: prometheus.DefBuckets,
		}, []string{"component"}),
		healthFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "samsara_component_health_check_failures_total",
			Help: "Total failed health checks per supervised component.",
		}, []string{"component"}),
	}
	reg.MustRegister(o.up, o.starts, o.restarts, o.stopErrors, o.healthDur, o.healthFailure)
	return o
}

// ComponentStarted implements samsara.MetricsObserver.
func (o *Observer) ComponentStarted(component string, _ int) {
	o.up.WithLabelValues(component).Set(1)
	o.starts.WithLabelValues(component).Inc()
}

// ComponentStopped implements samsara.MetricsObserver.
func (o *Observer) ComponentStopped(component string, err error) {
	o.up.WithLabelValues(component).Set(0)
	if err != nil {
		o.stopErrors.WithLabelValues(component).Inc()
	}
}

// ComponentRestarting implements samsara.MetricsObserver.
func (o *Observer) ComponentRestarting(component string, _ error, _ int, _ time.Duration) {
	o.up.WithLabelValues(component).Set(0)
	o.restarts.WithLabelValues(component).Inc()
}

// HealthCheckCompleted implements samsara.MetricsObserver.
func (o *Observer) HealthCheckCompleted(component string, duration time.Duration, err error) {
	o.healthDur.WithLabelValues(component).Observe(duration.Seconds())
	if err != nil {
		o.healthFailure.WithLabelValues(component).Inc()
	}
}
