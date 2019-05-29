package ping

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// BenchmarkProcessPacket-8   		 5000000	       271 ns/op // regular map[string]packetInfo
// BenchmarkProcessPacket-8		 5000000	       309 ns/op // with custom thread safe map
// BenchmarkProcessPacket-8   	 	 3000000	       458 ns/op // with sync.Map

func BenchmarkProcessPacket(b *testing.B) {
	p := &Pinger{
		size:        64,
		proto:       ProtocolICMP,
		wg:          &sync.WaitGroup{},
		packetsSent: pktMap{tex: &sync.Mutex{}, data: make(map[string]packetInfo)},
		packetsRcvd: pktMap{tex: &sync.Mutex{}, data: make(map[string]packetInfo)},
	}

	body := &icmp.Echo{
		ID:   1,
		Seq:  1,
		Data: bytes.Repeat([]byte{1}, int(p.size)),
	}

	message := &icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Code: 0,
		Body: body,
	}

	msgBytes, _ := message.Marshal(nil)

	pkt := &msg{
		data:   msgBytes,
		nbytes: len(msgBytes),
		ipAddr: "127.0.0.1",
		ttl:    24,
	}

	p.packetsSent.Store(pkt.ipAddr, packetInfo{
		sentAt:      map[uint]time.Time{1: time.Now().Add(-time.Second)},
		packetsSent: 1,
	})

	p.wg.Add(b.N)
	for k := 0; k < b.N; k++ {
		p.processPing(pkt)
	}
}
