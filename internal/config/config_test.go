package config

import (
	"os"
	"testing"
)

func TestLoadJSON(t *testing.T) {
	c := []byte(`{"version":1,"collector":{"poll_interval":"5s","timeout":"1s","retries":2},"modbus":{"registers":[{"name":"x","device":"127.0.0.1:502","unit_id":3,"address":40001,"type":"float32"}]},"output":{"victoriametrics":{"enabled":false}},"web":{"listen":"127.0.0.1:8080"}}`)
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, c, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Modbus.Registers[0].Address != 40001 {
		t.Fatal("unexpected address")
	}
}
