package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

func TestReusableLocalMotherConfigRequiresAnActiveMatchingCredential(t *testing.T) {
	stateDir := t.TempDir()
	master := bytes.Repeat([]byte{0x62}, 32)
	contextValue, token, tenant, err := edgepool.CreateMotherTenantCredential(
		stateDir,
		master,
		"Local QSDM Hive",
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(stateDir, "hive-mother.token")
	if err := edgepool.WriteTokenFile(tokenPath, token); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(stateDir, "mother-hive.json")
	config := motherHiveConfig{
		SchemaVersion:  1,
		RelayURL:       "http://127.0.0.1:7740",
		TokenFile:      tokenPath,
		ConnectionMode: "private-multi-hive",
		MotherID:       tenant.MotherID,
		MotherName:     tenant.MotherName,
		MotherContext:  contextValue,
	}
	if err := writeMotherHiveConfig(configPath, config); err != nil {
		t.Fatal(err)
	}

	loaded, ok := reusableLocalMotherConfig(configPath, stateDir, master)
	if !ok || loaded.MotherID != tenant.MotherID {
		t.Fatalf("active local Mother Hive config was not reusable: %+v", loaded)
	}
	if err := edgepool.RevokeMotherTenant(stateDir, tenant.MotherID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, ok := reusableLocalMotherConfig(configPath, stateDir, master); ok {
		t.Fatal("revoked local Mother Hive config was reused")
	}
}

func TestReusableLocalMotherConfigRejectsWrongToken(t *testing.T) {
	stateDir := t.TempDir()
	master := bytes.Repeat([]byte{0x63}, 32)
	contextValue, _, tenant, err := edgepool.CreateMotherTenantCredential(
		stateDir,
		master,
		"Local QSDM Hive",
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(stateDir, "hive-mother.token")
	if err := edgepool.WriteTokenFile(tokenPath, bytes.Repeat([]byte{0x64}, 32)); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(stateDir, "mother-hive.json")
	if err := writeMotherHiveConfig(configPath, motherHiveConfig{
		SchemaVersion: 1, TokenFile: tokenPath, ConnectionMode: "private-multi-hive",
		MotherID: tenant.MotherID, MotherName: tenant.MotherName, MotherContext: contextValue,
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := reusableLocalMotherConfig(configPath, stateDir, master); ok {
		t.Fatal("local Mother Hive config with the wrong token was reused")
	}
}
