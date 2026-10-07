package main

import (
	"bytes"
	"fmt"
	"runtime"

	"github.com/golang/protobuf/proto"
	pb "github.com/mame82/P4wnP1_aloa/proto"
	"google.golang.org/protobuf/encoding/protojson"
)

var unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}

const maxAPIBodyBytes = 8 << 20

func main() {
	var buf bytes.Buffer
	buf.WriteString(`{"TriggerActions":[`)
	n := 0
	for buf.Len()+3 < maxAPIBodyBytes-2 {
		if n > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString("{}")
		n++
	}
	buf.WriteString("]}")
	body := buf.Bytes()
	fmt.Printf("body bytes = %d (cap %d), elements in body = %d\n", len(body), maxAPIBodyBytes, n)

	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)

	msg := &pb.TriggerActionSet{}
	if err := unmarshalOpts.Unmarshal(body, proto.MessageV2(msg)); err != nil {
		fmt.Println("UNMARSHAL ERROR:", err)
		return
	}
	runtime.GC()
	runtime.ReadMemStats(&m1)

	mib := func(x uint64) float64 { return float64(x) / 1024 / 1024 }
	fmt.Printf("elements decoded = %d\n", len(msg.TriggerActions))
	fmt.Printf("HeapAlloc delta  = %.1f MiB\n", mib(m1.HeapAlloc-m0.HeapAlloc))
	fmt.Printf("TotalAlloc delta = %.1f MiB\n", mib(m1.TotalAlloc-m0.TotalAlloc))
	fmt.Printf("HeapSys          = %.1f MiB\n", mib(m1.HeapSys))
	fmt.Printf("Sys              = %.1f MiB\n", mib(m1.Sys))
	fmt.Printf("amplification (live) = %.1fx\n", float64(m1.HeapAlloc-m0.HeapAlloc)/float64(len(body)))
	runtime.KeepAlive(msg)
}
