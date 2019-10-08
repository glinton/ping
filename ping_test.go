package ping

import (
	"context"
	"fmt"
	"math"
	"net"
	"sync"
	"testing"
	"time"
)

func TestClient(t *testing.T) {
	req1, err := NewRequest("www.baidu.com")
	if err != nil {
		t.Error(err)
		return
	}
	req1.ID = 101
	req1.Seq = 201

	req2 := *req1
	req2.ID = 102
	req2.Seq = 202

	req3 := *req1
	req3.ID = 103
	req3.Seq = 203

	wg := new(sync.WaitGroup)
	wg.Add(3)
	go testClient(t, wg, req1)
	go testClient(t, wg, &req2)
	go testClient(t, wg, &req3)
	wg.Wait()
}

func testClient(t *testing.T, wg *sync.WaitGroup, req *Request) {
	defer wg.Done()

	resp, err := Do(context.Background(), req)
	if err != nil {
		t.Error(err)
	} else if resp.ID != req.ID || int(resp.Seq) != req.Seq {
		t.Error(req, resp)
	}
}

func TestE2E(t *testing.T) {
	hostIPs := []string{"8.8.8.8", "8.8.4.4", "1.1.1.1"}
	count := 3
	deadline := time.Second * 5
	timeout := time.Second * 2
	interval := time.Second
	pwg := &sync.WaitGroup{}

	for _, hostIP := range hostIPs {
		pwg.Add(1)
		go func(host string) {
			defer pwg.Done()

			tick := time.NewTicker(interval)
			defer tick.Stop()

			wg := &sync.WaitGroup{}
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()

			resps := make(chan *Response, count)
			packetsSent := 0

			for count == 0 || packetsSent < count {
				select {
				case <-ctx.Done():
					fmt.Println("deadline reached")
					return
				case <-tick.C:
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()

					packetsSent++
					wg.Add(1)
					go func(seq int) {
						defer wg.Done()
						resp, err := DefaultClient.Do(ctx, &Request{
							Dst: net.ParseIP(host),
							Seq: seq,
						})
						if err != nil {
							fmt.Println("failed to ping:", err)
							return
						}

						resps <- resp
						onRcv(resp)
					}(packetsSent)
				}
			}

			wg.Wait()
			close(resps)

			rsps := []*Response{}
			for res := range resps {
				rsps = append(rsps, res)
			}
			onFin(packetsSent, rsps)
			fmt.Println()
		}(hostIP)
	}
	pwg.Wait()
}

func onRcv(res *Response) {
	fmt.Printf("%d bytes from %s: icmp_seq=%d time=%v ttl=%v\n",
		res.TotalLength, res.Src.String(), res.Seq, res.RTT, res.TTL)
}

func onFin(packetsSent int, resps []*Response) {
	if len(resps) == 0 {
		fmt.Println("Sent:", packetsSent, "Received: 0")
		return
	}

	var min, max, avg, total time.Duration
	min = resps[0].RTT
	max = resps[0].RTT

	for _, res := range resps {
		if res.RTT < min {
			min = res.RTT
		}
		if res.RTT > max {
			max = res.RTT
		}
		total += res.RTT
	}

	avg = total / time.Duration(len(resps))
	var sumsquares time.Duration
	for _, res := range resps {
		sumsquares += (res.RTT - avg) * (res.RTT - avg)
	}
	stdDev := time.Duration(math.Sqrt(float64(sumsquares / time.Duration(len(resps)))))

	fmt.Printf("\n--- %s ping statistics ---\n", resps[0].Src.String())
	fmt.Printf("%d packets transmitted, %d packets received, %.2f%% packet loss\n",
		packetsSent, len(resps), float64(packetsSent-len(resps))/float64(packetsSent)*100)
	fmt.Printf("round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
		min, avg, max, stdDev)
}

func ExampleDo() {
	req, err := NewRequest("localhost")
	if err != nil {
		panic(err)
	}

	res, err := Do(context.Background(), req)
	if err != nil {
		panic(err)
	}

	// RTT is the time from an ICMP echo request to the time a reply is received.
	fmt.Println(res.RTT)
}

func ExampleIPv4() {
	res, err := IPv4(context.Background(), "google.com")
	if err != nil {
		panic(err)
	}

	// RTT is the time from an ICMP echo request to the time a reply is received.
	fmt.Println(res.RTT)
}

func ExampleNewRequest_withSource() {
	req, err := NewRequest("localhost")
	if err != nil {
		panic(err)
	}

	// If you have multiple interfaces spanning different networks
	// and want to ping from a specific interface, set the source.
	req.Src = net.ParseIP("127.0.0.2")

	res, err := Do(context.Background(), req)
	if err != nil {
		panic(err)
	}

	// RTT is the time from an ICMP echo request to the time a reply is received.
	fmt.Println(res.RTT)
}
