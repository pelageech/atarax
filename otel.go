package atarax

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	OTelScopeName = "github.com/pelageech/atarax"
	MeterPrefix   = "atarax."
)

var (
	_attributeUnspecified = metric.WithAttributeSet(attribute.NewSet(attribute.String(MeterPrefix+"jobs.status", "UNSPECIFIED")))
	_attributeTimeout     = metric.WithAttributeSet(attribute.NewSet(attribute.String(MeterPrefix+"jobs.status", "TIMEOUT")))
	_attributeErr         = metric.WithAttributeSet(attribute.NewSet(attribute.String(MeterPrefix+"jobs.status", "ERR")))
	_attributeOK          = metric.WithAttributeSet(attribute.NewSet(attribute.String(MeterPrefix+"jobs.status", "OK")))

	_ataraxInfo, _  = Meter().Int64Gauge(MeterPrefix + "info")
	_jobTimings, _  = Meter().Int64Histogram(MeterPrefix + "jobs.timings")
	_jobsWorking, _ = Meter().Int64UpDownCounter(MeterPrefix + "jobs.working")

	_ataraxService = attribute.String(MeterPrefix+"service", "unspecified")
	_ataraxSystem  = attribute.String(MeterPrefix+"system", "unspecified")
	_ataraxVersion = attribute.String(MeterPrefix+"version", Version())

	_attributes = metric.WithAttributes(
		_ataraxService, _ataraxSystem, _ataraxVersion,
	)
)

// Meter returns an instrumented meter with the scope OTelScopeName.
func Meter() metric.Meter {
	return otel.Meter(OTelScopeName, metric.WithInstrumentationAttributes(SystemAttributes()...))
}

func InstrumentMetrics() {
	_ataraxInfo, _ = Meter().Int64Gauge(MeterPrefix + "info")
	_ataraxInfo.Record(context.TODO(), 1, WithSystemAttributes())

	_jobTimings, _ = Meter().Int64Histogram(MeterPrefix+"jobs.timings", metric.WithUnit("ms"))
	_jobsWorking, _ = Meter().Int64UpDownCounter(MeterPrefix + "jobs.working")
}

func SystemAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		_ataraxService, _ataraxSystem, _ataraxVersion,
	}
}

func WithSystemAttributes() metric.MeasurementOption {
	return _attributes
}

// SetService configures atarax.service attribute for atarax.info.
func SetService(name string) {
	_ataraxService = attribute.String(MeterPrefix+"service", name)
	setSystemAttributes()
}

// SetSystem configures atarax.system attribute for atarax.info.
func SetSystem(system string) {
	_ataraxSystem = attribute.String(MeterPrefix+"system", system)
	setSystemAttributes()
}

// SetVersion configures atarax.version attribute for atarax.info.
func SetVersion(version string) {
	_ataraxVersion = attribute.String(MeterPrefix+"version", version)
	setSystemAttributes()
}

func setSystemAttributes() {
	_attributes = metric.WithAttributes(
		_ataraxService, _ataraxSystem, _ataraxVersion,
	)
}
