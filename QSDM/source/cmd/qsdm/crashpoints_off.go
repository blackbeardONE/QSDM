//go:build !hl_crashpoints

package main

// crashpoints_off.go (HL1 WP9): the production build has no crash or fault
// injection. See crashpoints_hl.go (build tag hl_crashpoints).

import "github.com/blackbeardONE/QSDM/internal/legacymining"

const hl1CrashpointsEnabled = false

func hl1Crashpoint(string) {}

func hl1Fault(string) error { return nil }

func hl1CrashInsideD1(string, string, string) {}

func hl1CrashpointStore(s legacymining.Store) legacymining.Store { return s }
