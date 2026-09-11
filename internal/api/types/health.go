package types

const (
	HealthStatusOK       = "ok"
	HealthStatusDegraded = "degraded"
)

func HealthStatusServing(status string) bool {
	return status == HealthStatusOK || status == HealthStatusDegraded
}
