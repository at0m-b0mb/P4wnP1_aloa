package main

import (
	"fmt"
	"runtime"
	"strings"

	pb "github.com/mame82/P4wnP1_aloa/proto"
	"google.golang.org/protobuf/encoding/protojson"
	gproto "github.com/golang/protobuf/proto"
)

var unmarshalOpts = protojson.UnmarshalOptions{DiscardUnknown: true}

func main() {
	const cap = 8 << 20
	prefix := `{"TriggerActions":[`
	suffix := `]}`
	room := cap - len(prefix) - len(suffix)
	n := (room + 1) / 3 // "{}," each 3 bytes, last has no comma
	var sb strings.Builder
	sb.Grow(cap + 16)
	sb.WriteString(prefix)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("{}")
	}
	sb.WriteString(suffix)
	body := sb.String()
	if len(body) > cap {
		panic(fmt.Sprintf("too big: %d", len(body)))
	}

	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)

	msg := &pb.TriggerActionSet{}
	if err := unmarshalOpts.Unmarshal([]byte(body), gproto.MessageV2(msg)); err != nil {
		fmt.Println("UNMARSHAL ERROR:", err)
		return
	}

	runtime.GC()
	runtime.ReadMemStats(&m1)
	fmt.Printf("body bytes            = %d (cap %d)\n", len(body), cap)
	fmt.Printf("elements decoded      = %d\n", len(msg.TriggerActions))
	fmt.Printf("HeapAlloc delta       = %.1f MiB\n", float64(m1.HeapAlloc-m0.HeapAlloc)/(1<<20))
	fmt.Printf("TotalAlloc delta      = %.1f MiB\n", float64(m1.TotalAlloc-m0.TotalAlloc)/(1<<20))
	fmt.Printf("HeapSys               = %.1f MiB\n", float64(m1.HeapSys)/(1<<20))
	fmt.Printf("amplification (live)  = %.1fx\n", float64(m1.HeapAlloc-m0.HeapAlloc)/float64(len(body)))
	_ = msg.TriggerActions[0]
	runtime.KeepAlive(msg)
}
