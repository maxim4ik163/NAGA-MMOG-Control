package main

import "testing"

func TestFirmwareRequest(t *testing.T) {
	r := newRazerReport(0xFF, 0x00, 0x81, 0x02)
	if r[1] != 0xFF || r[5] != 0x02 || r[6] != 0x00 || r[7] != 0x81 {
		t.Fatalf("header %X", r[:8])
	}
	if r[88] != 0x02^0x81 {
		t.Fatalf("crc %02X", r[88])
	}
}

func TestLEDStateRequest(t *testing.T) {
	r := newRazerReport(0xFF, 0x03, 0x80, 0x03, 0x01, 0x05)
	if r[8] != 0x01 || r[9] != 0x05 || r[88] != 0x03^0x03^0x80^0x01^0x05 {
		t.Fatalf("bad %X crc %02X", r[:10], r[88])
	}
}
