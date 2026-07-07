//go:build !windows && !linux

package main

import "errors"

func configureAutoStart(enabled bool, executable string) error {
	if enabled {
		return errors.New("start at sign-in is currently supported on Windows and Linux")
	}
	return nil
}
