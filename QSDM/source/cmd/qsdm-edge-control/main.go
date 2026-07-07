package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-version":
			fmt.Printf("qsdm-edge-control %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
			return 0
		}
	}
	flags := flag.NewFlagSet("qsdm-edge-control", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:7741", "local control address")
	noOpen := flags.Bool("no-open", false, "do not open the control window")
	autoStart := flags.Bool("auto-start", false, "start the saved Agent or Relay after sign-in")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if !loopbackAddress(*listen) {
		fmt.Fprintln(os.Stderr, "qsdm-edge-control: the control window must listen on localhost")
		return 1
	}

	paths, err := defaultControlPaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-edge-control:", err)
		return 1
	}
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-edge-control:", err)
		return 1
	}
	controlToken, err := ensureToken(paths.ControlToken)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-edge-control:", err)
		return 1
	}
	token := hex.EncodeToString(controlToken)
	baseURL := "http://" + *listen
	windowURL := baseURL + "/?t=" + token

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		if probeExistingControl(baseURL, token) {
			if !*noOpen {
				_ = openBrowser(windowURL)
			}
			return 0
		}
		fmt.Fprintf(os.Stderr, "qsdm-edge-control: %s is already in use by another application\n", *listen)
		return 1
	}
	defer listener.Close()

	settings, err := loadSettings(paths.SettingsFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-edge-control:", err)
		return 1
	}
	controller := newController(paths, settings, version)
	quit := make(chan struct{}, 1)
	controlUI, err := newControlServer(controller, token, quit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-edge-control:", err)
		return 1
	}
	server := &http.Server{
		Handler:           controlUI.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	if *autoStart && settings.AutoStart {
		if err := controller.start(); err != nil {
			controller.setError(err)
		}
	}
	if !*noOpen {
		if err := openBrowser(windowURL); err != nil {
			fmt.Fprintln(os.Stderr, "qsdm-edge-control: open window:", err)
		}
	}
	fmt.Printf("QSDM Edge Control %s ready at %s\n", version, baseURL)

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	select {
	case <-quit:
	case <-signalContext.Done():
	case serveErr := <-serverErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "qsdm-edge-control:", serveErr)
		}
	}
	_ = controller.stop()
	shutdownHTTPServer(server)
	return 0
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openBrowser(address string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		command = exec.Command("open", address)
	default:
		command = exec.Command("xdg-open", address)
	}
	return command.Start()
}
