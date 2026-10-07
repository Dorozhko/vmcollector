package modbus

import "testing"

func TestModbusOffset(t *testing.T) {
	cases := map[uint16]uint16{40001: 0, 40002: 1, 40100: 99, 0: 0, 123: 123}
	for in, want := range cases {
		if got := modbusOffset(in); got != want {
			t.Fatalf("%d -> %d, want %d", in, got, want)
		}
	}
}
