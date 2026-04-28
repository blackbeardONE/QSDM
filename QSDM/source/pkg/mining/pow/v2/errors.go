package powv2

import "errors"

var (
	errNilDAG   = errors.New("powv2: dag must not be nil")
	errEmptyDAG = errors.New("powv2: dag has zero entries")
)
