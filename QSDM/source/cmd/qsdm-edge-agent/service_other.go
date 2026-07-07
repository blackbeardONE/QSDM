//go:build !linux

package main

import "errors"

func installAgentService(string) (string, error) {
	return "", errors.New("install-service is available only in the Linux agent")
}

func uninstallAgentService() error {
	return errors.New("uninstall-service is available only in the Linux agent")
}

func showAgentServiceStatus() error {
	return errors.New("service-status is available only in the Linux agent")
}

func installCoordinatorService(coordinatorServiceOptions) (string, error) {
	return "", errors.New("install-relay-service is available only in the Linux agent")
}

func uninstallCoordinatorService() error {
	return errors.New("uninstall-relay-service is available only in the Linux agent")
}

func showCoordinatorServiceStatus() error {
	return errors.New("relay-service-status is available only in the Linux agent")
}
