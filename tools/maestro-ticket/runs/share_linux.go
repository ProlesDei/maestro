package runs

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// sock is one socket as the kernel lists it.
type sock struct {
	listen bool
	// at is where it listens or, for an accepted connection, where the
	// connection came in: a unix socket's name, a TCP socket's port.
	at string
	// peer is the inode of the socket at the other end, 0 if none or not
	// on this machine.
	peer uint64
}

// sharedProcs returns those of a run's processes, procs, that serve a
// process outside the run, and every one they started: a daemon the run
// started first that other sessions now share, such as a Gradle daemon,
// an sccache server or an ssh ControlPersist master. Killing one fails
// the sessions using it. Nothing else tells one from a run's own server
// left running with setsid: both lead a session of their own under init,
// with no terminal and their output sent to /dev/null.
func sharedProcs(procs []int) map[int]bool {
	held := map[int][]uint64{}
	for _, pid := range procs {
		held[pid] = sockets(pid)
	}
	some := false
	for _, s := range held {
		some = some || len(s) > 0
	}
	if !some {
		return nil
	}
	socks := map[uint64]sock{}
	unixSocks(socks)
	tcpSocks(socks)
	parent := map[int]int{}
	for _, pid := range procs {
		parent[pid] = ppid(pid)
	}
	return serving(held, socks, parent)
}

// serving picks, of the processes in held, each that holds a listening
// socket and a connection accepted on it whose other end none of them
// holds, and each process below one of those in parent.
func serving(held map[int][]uint64, socks map[uint64]sock, parent map[int]int) map[int]bool {
	mine := map[uint64]bool{}
	for _, inos := range held {
		for _, ino := range inos {
			mine[ino] = true
		}
	}
	serves := map[int]bool{}
	for pid, inos := range held {
		listens := map[string]bool{}
		for _, ino := range inos {
			if s := socks[ino]; s.listen {
				listens[s.at] = true
			}
		}
		for _, ino := range inos {
			if s := socks[ino]; !s.listen && s.peer != 0 && listens[s.at] && !mine[s.peer] {
				serves[pid] = true
			}
		}
	}
	out := map[int]bool{}
	for pid := range held {
		seen := map[int]bool{}
		for p := pid; p > 1 && !seen[p]; p = parent[p] {
			seen[p] = true
			if serves[p] {
				out[pid] = true
				break
			}
		}
	}
	return out
}

// sockets lists the inodes of the sockets pid holds open.
func sockets(pid int) []uint64 {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []uint64
	for _, e := range ents {
		l, err := os.Readlink(dir + "/" + e.Name())
		if n, ok := strings.CutPrefix(l, "socket:["); err == nil && ok {
			if ino, err := strconv.ParseUint(strings.TrimSuffix(n, "]"), 10, 64); err == nil {
				out = append(out, ino)
			}
		}
	}
	return out
}

// ppid returns pid's parent, 0 if it can't be read.
func ppid(pid int) int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	i := strings.LastIndexByte(string(b), ')')
	f := strings.Fields(string(b[i+1:])) // state is f[0], ppid f[1]
	if i < 0 || len(f) < 2 {
		return 0
	}
	p, _ := strconv.Atoi(f[1])
	return p
}

// Socket states, as the kernel numbers them for TCP and unix sockets alike.
const (
	stateEstablished = 1
	stateListen      = 10
)

// unixSocks adds every listening and connected unix socket, read through
// sock_diag, the one place the kernel names a unix socket's peer. An
// accepted connection carries the name of the socket it came in on; a
// client's end has none unless it bound one.
func unixSocks(out map[uint64]sock) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return
	}
	defer unix.Close(fd)
	const (
		showName = 1 // UDIAG_SHOW_NAME
		showPeer = 4 // UDIAG_SHOW_PEER
		attrName = 0 // UNIX_DIAG_NAME
		attrPeer = 2 // UNIX_DIAG_PEER
	)
	// A netlink header, then struct unix_diag_req.
	req := make([]byte, unix.NLMSG_HDRLEN+24)
	ne := binary.NativeEndian
	ne.PutUint32(req[0:], uint32(len(req)))
	ne.PutUint16(req[4:], unix.SOCK_DIAG_BY_FAMILY)
	ne.PutUint16(req[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	r := req[unix.NLMSG_HDRLEN:]
	r[0] = unix.AF_UNIX
	ne.PutUint32(r[4:], 1<<stateEstablished|1<<stateListen)
	ne.PutUint32(r[12:], showName|showPeer)
	if unix.Sendto(fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}) != nil {
		return
	}
	buf := make([]byte, 1<<16)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return
		}
		for b := buf[:n]; len(b) >= unix.NLMSG_HDRLEN; {
			l := int(ne.Uint32(b[0:]))
			typ := ne.Uint16(b[4:])
			if l < unix.NLMSG_HDRLEN || l > len(b) || typ == unix.NLMSG_DONE || typ == unix.NLMSG_ERROR {
				return
			}
			// struct unix_diag_msg, then its attributes.
			if m := b[unix.NLMSG_HDRLEN:l]; len(m) >= 16 {
				s := sock{listen: m[2] == stateListen}
				for a := m[16:]; len(a) >= 4; {
					al := int(ne.Uint16(a[0:]))
					if al < 4 || al > len(a) {
						break
					}
					switch v := a[4:al]; ne.Uint16(a[2:]) {
					case attrName:
						s.at = "unix:" + string(v)
					case attrPeer:
						if len(v) >= 4 {
							s.peer = uint64(ne.Uint32(v))
						}
					}
					a = a[min(len(a), (al+3)&^3):]
				}
				out[uint64(ne.Uint32(m[4:]))] = s
			}
			b = b[min(len(b), (l+3)&^3):]
		}
	}
}

// tcpSocks adds every listening and established TCP socket. A peer counts
// only when its end is on this machine too: the socket whose two ends are
// this one's swapped.
func tcpSocks(out map[uint64]sock) {
	type conn struct {
		ino           uint64
		listen        bool
		local, remote netip.AddrPort
	}
	var conns []conn
	byEnds := map[[2]netip.AddrPort]uint64{}
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // the header
		for sc.Scan() {
			// sl local remote st tx:rx tr:tm retrnsmt uid timeout inode
			fl := strings.Fields(sc.Text())
			if len(fl) < 10 {
				continue
			}
			st, err1 := strconv.ParseUint(fl[3], 16, 8)
			ino, err2 := strconv.ParseUint(fl[9], 10, 64)
			local, ok1 := hexAddr(fl[1])
			remote, ok2 := hexAddr(fl[2])
			if err1 != nil || err2 != nil || !ok1 || !ok2 || ino == 0 || (st != stateEstablished && st != stateListen) {
				continue
			}
			c := conn{ino, st == stateListen, local, remote}
			conns = append(conns, c)
			if !c.listen {
				byEnds[[2]netip.AddrPort{local, remote}] = ino
			}
		}
		f.Close()
	}
	for _, c := range conns {
		s := sock{listen: c.listen, at: fmt.Sprintf("tcp:%d", c.local.Port())}
		if !c.listen {
			s.peer = byEnds[[2]netip.AddrPort{c.remote, c.local}]
		}
		out[c.ino] = s
	}
}

// hexAddr reads an address as /proc/net/tcp writes it: the IP as 32-bit
// words in host order, in hex, then a colon and the port in hex. An IPv4
// address mapped into IPv6 comes back as IPv4.
func hexAddr(s string) (netip.AddrPort, bool) {
	h, p, ok := strings.Cut(s, ":")
	b, err := hex.DecodeString(h)
	port, err2 := strconv.ParseUint(p, 16, 16)
	if !ok || err != nil || err2 != nil || (len(b) != 4 && len(b) != 16) {
		return netip.AddrPort{}, false
	}
	for i := 0; i < len(b); i += 4 {
		binary.BigEndian.PutUint32(b[i:], binary.NativeEndian.Uint32(b[i:]))
	}
	ip, _ := netip.AddrFromSlice(b)
	return netip.AddrPortFrom(ip.Unmap(), uint16(port)), true
}
