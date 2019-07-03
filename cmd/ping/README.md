## ping

Send ICMP ECHO_REQUEST to network hosts

`ping` uses the ICMP protocol's mandatory ECHO_REQUEST datagram to elicit an ICMP ECHO_RESPONSE from a host or gateway. ECHO_REQUEST datagrams (pings) have an IP and ICMP header, followed by an arbitrary number of padbytes used to fill out the packet.

`ping` works with both IPv4 and IPv6. Using only one of them explicitly can be enforced by specifying -4 or -6.


#### Installation

```sh
go get github.com/glinton/ping/cmd/ping
# to run:
$GOPATH/bin/ping google.com
```


#### Usage
```
ping [-h] [-4] [-6] [-c count] [-i interval] [-I interface]
     [-w deadline] [-W timeout] [-s packetsize] destination

-4
    Use IPv4 only.

-6
    Use IPv6 only.

-c count
    Stop after sending count ECHO_REQUEST packets. With deadline
    option, ping waits for count ECHO_REPLY packets, until the timeout
    expires.

-h
    Show help.

-i interval
    Wait interval seconds between sending each packet. The default is
    to wait for one second between each packet normally. Interval values
    less than 200 milliseconds (200ms) are not allowed.

-I interface
    interface is either an address, or an interface name. If interface
    is an address, it sets source address to specified interface
    address. If interface in an interface name, it sets source
    interface to specified interface.

-s packetsize
    Specifies the number of data bytes to be sent. The default is 56,
    which translates into 64 ICMP data bytes when combined with the 8
    bytes of ICMP header data.

-w deadline
    Specify a timeout, in seconds, before ping exits regardless of how
    many packets have been sent or received. In this case ping does not
    stop after count packet are sent, it waits either for deadline
    expire or until count probes are answered or for some error
    notification from network.

-W timeout
    Time to wait for a response, in seconds. The option affects only
    timeout in absence of any responses.
```


#### Examples

```
# ping google continuously
ping google.com

# ping google 5 times
ping -c 5 google.com

# ping google 5 times at 500ms intervals
ping -c 5 -i .5 google.com

# ping google for 10 seconds
ping -w 10 google.com
```


#### Notes Regarding ICMP Socket Permissions

System installed `ping` binaries generally have `setuid` attributes set, thus allowing them to utilize privileged ICMP sockets. This should work for applications built with this library as well, but a better approach would be to give the application the capability to create privileged ICMP sockets. To do so, run the following as the `root` user (not applicable to Windows).

```
setcap cap_net_raw=eip /gopath/bin/ping
```

If you desire to utilize unprivileged raw sockets on Linux, the system group of the user running ping must be allowed to create unprivileged ICMP sockets. [See man pages icmp(7) for `ping_group_range`](http://man7.org/linux/man-pages/man7/icmp.7.html).

To allow a range of groups access to create unprivileged icmp sockets on linux (ipv4 or ipv6), run:

```
sudo sysctl -w net.ipv4.ping_group_range="GROUPID_START GROUPID_END"
```

If you plan to run your application as `root`, the aforementioned commmand is not necessary.

On Windows, running a terminal as admin should not be necessary.
