## ping

[![GoDoc](https://godoc.org/github.com/glinton/ping?status.svg)](https://godoc.org/github.com/glinton/ping)

Concurrent-first ping (ICMP) library for go.

Inspired by [go-ping](https://github.com/sparrc/go-ping)


### OS Specific Notes

#### Linux

Since this library can utilize unprivileged raw sockets on Linux and Darwin (`udp4` or `udp6` as the network passed to `Listen`). On Linux, the system group of the user running the application must be allowed to create ICMP Echo sockets. [See man pages icmp(7) for `ping_group_range`](http://man7.org/linux/man-pages/man7/icmp.7.html).

To allow a range of groups access to create icmp sockets on linux (ipv4 or ipv6), run:
```
sudo sysctl -w net.ipv4.ping_group_range="GROUPID_START   GROUPID_END"
```

If you plan to run your application as `root`, the aforementioned commmand is not necessary.

#### Windows

When running on Windows, you must call `Listen` with either `ip4:icmp` or `ip6:ipv6-icmp` as the network to avoid receiving the following error:

```
Error listening for ICMP packets: socket: The requested protocol has not been configured into the system, or no implementation for it exists.
```

Running a terminal as admin should not be necessary.


### Known Issues

There is currently no support for TTL on windows, track progress at https://github.com/golang/go/issues/7175 and https://github.com/golang/go/issues/7174

Report any other issues you may find [here](https://github.com/glinton/ping/issues/new)

