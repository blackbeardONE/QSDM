package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

func TestAgentRelayControllerLifecycle(t *testing.T) {
	port := reservePort(t)
	relayPaths := testControlPaths(t, "relay")
	relaySettings := defaultSettings()
	relaySettings.Role = "relay"
	relaySettings.Relay.Port = port
	relaySettings.Relay.AllowLAN = false
	relay := newController(relayPaths, relaySettings, "test")
	relay.autoStart = func(bool, string) error { return nil }
	if err := relay.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.stop() })

	codes, err := relay.getPairingCodes()
	if err != nil {
		t.Fatal(err)
	}
	agentPaths := testControlPaths(t, "agent")
	agentSettings := defaultSettings()
	agent := newController(agentPaths, agentSettings, "test")
	agent.autoStart = func(bool, string) error { return nil }
	if err := agent.pairAgent(codes.AgentCode); err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	updated := agent.settings
	agent.mu.Unlock()
	updated.Agent.RAM = false
	updated.Agent.CPUShare = 10
	if err := agent.updateSettings(updated); err != nil {
		t.Fatal(err)
	}
	if err := agent.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agent.stop() })

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := relay.snapshot()
		if snapshot.Relay != nil && len(snapshot.Relay.Workers) == 1 && snapshot.Relay.ReceiptCounts[edgepool.ResourceCPU] > 0 {
			if err := relay.connectLocalMother(); err != nil {
				t.Fatal(err)
			}
			if !relay.snapshot().Connections.MotherConfigured {
				t.Fatal("Mother Hive connection was not persisted")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Agent did not complete Relay work; Relay snapshot: %+v", relay.snapshot().Relay)
}

func testControlPaths(t *testing.T, name string) controlPaths {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	pool := filepath.Join(root, "pool")
	return controlPaths{
		ConfigDir:    root,
		SettingsFile: filepath.Join(root, "settings.json"),
		ControlToken: filepath.Join(root, "control.token"),
		PoolDir:      pool,
		AgentToken:   filepath.Join(pool, "agent.token"),
		MotherToken:  filepath.Join(pool, "mother-hive.token"),
		MotherConfig: filepath.Join(pool, "mother-hive.json"),
		AgentLog:     filepath.Join(pool, "agent.log"),
		Executable:   os.Args[0],
	}
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}
