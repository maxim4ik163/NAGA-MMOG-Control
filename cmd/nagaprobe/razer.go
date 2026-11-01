package main

// Razer control report, re-implemented from the public description of the
// protocol (90-byte feature report, report ID 0):
//
//	0      status (request: 0; response: 1 busy, 2 ok, 3 fail, 4 timeout, 5 not supported)
//	1      transaction id
//	2..3   remaining packets (u16 BE)
//	4      protocol type (0)
//	5      data size (number of argument bytes)
//	6      command class
//	7      command id (bit 7 set = get)
//	8..87  arguments
//	88     checksum: XOR of bytes 2..87
//	89     reserved

import "fmt"

const razerReportLen = 90

type razerReport [razerReportLen]byte

func newRazerReport(tid, class, id, size byte, args ...byte) razerReport {
	var r razerReport
	r[1] = tid
	r[5] = size
	r[6] = class
	r[7] = id
	copy(r[8:88], args)
	r[88] = r.checksum()
	return r
}

func (r *razerReport) checksum() byte {
	var c byte
	for i := 2; i < 88; i++ {
		c ^= r[i]
	}
	return c
}

func (r *razerReport) args() []byte { return r[8:88] }

var statusNames = map[byte]string{
	0: "new", 1: "busy", 2: "ok", 3: "failure", 4: "timeout", 5: "not supported",
}

func statusName(s byte) string {
	if n, ok := statusNames[s]; ok {
		return n
	}
	return fmt.Sprintf("0x%02X", s)
}
