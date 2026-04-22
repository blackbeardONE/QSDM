package monitoring

import "fmt"

// String formatting helpers shared by Prometheus-related code.

func fmtUint(v uint64) string {
	return fmt.Sprintf("%d", v)
}

func fmtInt(v int) string {
	return fmt.Sprintf("%d", v)
}

func fmtInt64(v int64) string {
	return fmt.Sprintf("%d", v)
}
