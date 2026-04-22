#!/bin/bash
export PKG_CONFIG_PATH=/c/liboqs-config
export QSDMPLUS_METRICS_REGISTER_STRICT=1
go test ./wasm_modules/wallet
