Integration tests were moved into the Go module at **`QSDM/source/tests/`** so `go test ./...` from `QSDM/source` includes them.

```bash
cd QSDM/source
export QSDM_METRICS_REGISTER_STRICT=1
CGO_ENABLED=0 go test ./... ./tests/... -short
```
