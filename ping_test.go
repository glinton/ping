package ping_test

import (
	"context"
	"fmt"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/glinton/ping"
)

func TestE2E(t *testing.T) {
	c := &ping.Client{}

	hostIPs := []string{"8.8.8.8", "8.8.4.4", "1.1.1.1", "10.10.10.10", "127.0.0.1"}
	count := 3
	deadline := time.Second * 5
	timeout := time.Second * 2
	interval := time.Second
	pwg := &sync.WaitGroup{}

	for xx, hostIP := range hostIPs {
		pwg.Add(1)
		go func(x int, host string) {
			defer pwg.Done()

			tick := time.NewTicker(interval)
			defer tick.Stop()

			wg := &sync.WaitGroup{}
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()

			resps := make(chan *ping.Response, count)
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
					go func(id, seq int) {
						defer wg.Done()
						resp, err := c.Do(ctx, &ping.Request{
							Dst: net.ParseIP(host),
							ID:  id + 1,
							Seq: seq,
						})
						if err != nil {
							fmt.Println("failed to ping:", err)
							return
						}

						resps <- resp
						onRcv(resp)
					}(x, packetsSent)
				}
			}

			wg.Wait()
			close(resps)

			rsps := []*ping.Response{}
			for res := range resps {
				rsps = append(rsps, res)
			}
			onFin(packetsSent, rsps)
			fmt.Println()
		}(xx, hostIP)
	}
	pwg.Wait()
}

func onRcv(res *ping.Response) {
	fmt.Printf("%d bytes from %s: icmp_seq=%d time=%v ttl=%v\n",
		res.TotalLength, res.Req.Dst.String(), res.Seq, res.RTT, res.TTL)
}

func onFin(packetsSent int, resps []*ping.Response) {
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

	fmt.Printf("\n--- %s ping statistics ---\n", resps[0].Req.Dst.String())
	fmt.Printf("%d packets transmitted, %d packets received, %.2f%% packet loss\n",
		packetsSent, len(resps), float64(packetsSent-len(resps))/float64(packetsSent)*100)
	fmt.Printf("round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
		min, avg, max, stdDev)
}

func ExampleDo() {
	req, err := ping.NewRequest("localhost")
	if err != nil {
		panic(err)
	}

	res, err := ping.Do(context.Background(), req)
	if err != nil {
		panic(err)
	}

	// RTT is the time from an ICMP echo request to the time a reply is received.
	fmt.Println(res.RTT)
}

func ExampleIPv4() {
	res, err := ping.IPv4(context.Background(), "google.com")
	if err != nil {
		panic(err)
	}

	// RTT is the time from an ICMP echo request to the time a reply is received.
	fmt.Println(res.RTT)
}

func ExampleNewRequest_withSource() {
	req, err := ping.NewRequest("localhost")
	if err != nil {
		panic(err)
	}

	// If you have multiple interfaces spanning different networks
	// and want to ping from a specific interface, set the source.
	req.Src = net.ParseIP("127.0.0.2")

	res, err := ping.Do(context.Background(), req)
	if err != nil {
		panic(err)
	}

	// RTT is the time from an ICMP echo request to the time a reply is received.
	fmt.Println(res.RTT)
}
