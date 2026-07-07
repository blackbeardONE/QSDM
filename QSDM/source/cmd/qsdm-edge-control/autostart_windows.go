//go:build windows

package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const autoStartRegistryName = "QSDM Edge Control"

func configureAutoStart(enabled bool, executable string) error {
	const keyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	var key registry.Key
	var err error
	if enabled {
		key, _, err = registry.CreateKey(
			registry.CURRENT_USER,
			keyPath,
			registry.QUERY_VALUE|registry.SET_VALUE,
		)
	} else {
		key, err = registry.OpenKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		err = key.DeleteValue(autoStartRegistryName)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	command := fmt.Sprintf("\"%s\" --no-open --auto-start", executable)
	return key.SetStringValue(autoStartRegistryName, command)
}
