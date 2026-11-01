package outbounds

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/txthinking/socks5"
)

func TestSOCKS5OutboundUDPAssociateForwardsRequestAndResponse(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echoDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 2048)
		n, peer, err := echo.ReadFromUDP(buf)
		if err == nil {
			_, err = echo.WriteToUDP(append([]byte("echo:"), buf[:n]...), peer)
		}
		echoDone <- err
	}()

	relay, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	relayDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		n, client, err := relay.ReadFromUDP(buf)
		if err != nil {
			relayDone <- err
			return
		}
		datagram, err := socks5.NewDatagramFromBytes(buf[:n])
		if err != nil {
			relayDone <- err
			return
		}
		destination := socks5AddrToAddrEx(datagram.Atyp, datagram.DstAddr, datagram.DstPort)
		if destination.String() != echo.LocalAddr().String() {
			relayDone <- fmt.Errorf("unexpected UDP destination %s", destination.String())
			return
		}
		remote, err := net.DialTimeout("udp4", echo.LocalAddr().String(), time.Second)
		if err != nil {
			relayDone <- err
			return
		}
		defer remote.Close()
		_ = remote.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err = remote.Write(datagram.Data); err != nil {
			relayDone <- err
			return
		}
		response := make([]byte, 2048)
		responseN, err := remote.Read(response)
		if err != nil {
			relayDone <- err
			return
		}
		reply := socks5.NewDatagram(datagram.Atyp, datagram.DstAddr, datagram.DstPort, response[:responseN])
		_, err = relay.WriteToUDP(reply.Bytes(), client)
		relayDone <- err
	}()

	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	controlDone := make(chan error, 1)
	go func() { controlDone <- serveOneSOCKS5UDPAssociate(tcp, relay.LocalAddr().(*net.UDPAddr)) }()

	outbound := NewSOCKS5Outbound(tcp.Addr().String(), "", "")
	udp, err := outbound.UDP(&AddrEx{Host: echo.LocalAddr().(*net.UDPAddr).IP.String(), Port: uint16(echo.LocalAddr().(*net.UDPAddr).Port)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	if concrete, ok := udp.(*socks5UDPConn); ok {
		_ = concrete.udpConn.SetDeadline(time.Now().Add(3 * time.Second))
	}
	payload := []byte("hui-user-route-poc")
	if _, err = udp.WriteTo(payload, &AddrEx{Host: "127.0.0.1", Port: uint16(echo.LocalAddr().(*net.UDPAddr).Port)}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2048)
	n, from, err := udp.ReadFrom(response)
	if err != nil {
		t.Fatal(err)
	}
	if from.String() != echo.LocalAddr().String() {
		t.Fatalf("response source = %s", from.String())
	}
	if !bytes.Equal(response[:n], append([]byte("echo:"), payload...)) {
		t.Fatalf("response = %q", response[:n])
	}

	for name, done := range map[string]<-chan error{"echo": echoDone, "relay": relayDone} {
		select {
		case err = <-done:
			if err != nil {
				t.Fatalf("%s failed: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s timed out", name)
		}
	}
	_ = udp.Close()
	select {
	case err = <-controlDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SOCKS5 control connection did not close")
	}
}

func serveOneSOCKS5UDPAssociate(listener net.Listener, relay *net.UDPAddr) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 2)
	if _, err = io.ReadFull(conn, header); err != nil {
		return err
	}
	methods := make([]byte, int(header[1]))
	if _, err = io.ReadFull(conn, methods); err != nil {
		return err
	}
	if header[0] != socks5.Ver || !bytes.Contains(methods, []byte{socks5.MethodNone}) {
		return fmt.Errorf("unexpected SOCKS5 negotiation")
	}
	if _, err = conn.Write([]byte{socks5.Ver, socks5.MethodNone}); err != nil {
		return err
	}
	requestHeader := make([]byte, 4)
	if _, err = io.ReadFull(conn, requestHeader); err != nil {
		return err
	}
	if requestHeader[0] != socks5.Ver || requestHeader[1] != socks5.CmdUDP {
		return fmt.Errorf("expected UDP ASSOCIATE, got command %d", requestHeader[1])
	}
	if err = discardSOCKS5Address(conn, requestHeader[3]); err != nil {
		return err
	}
	port := make([]byte, 2)
	if _, err = io.ReadFull(conn, port); err != nil {
		return err
	}
	relayPort := make([]byte, 2)
	binary.BigEndian.PutUint16(relayPort, uint16(relay.Port))
	reply := append([]byte{socks5.Ver, socks5.RepSuccess, 0, socks5.ATYPIPv4}, relay.IP.To4()...)
	reply = append(reply, relayPort...)
	if _, err = conn.Write(reply); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, conn)
	return err
}

func discardSOCKS5Address(r io.Reader, atyp byte) error {
	length := 0
	switch atyp {
	case socks5.ATYPIPv4:
		length = 4
	case socks5.ATYPIPv6:
		length = 16
	case socks5.ATYPDomain:
		one := []byte{0}
		if _, err := io.ReadFull(r, one); err != nil {
			return err
		}
		length = int(one[0])
	default:
		return fmt.Errorf("unsupported address type %d", atyp)
	}
	_, err := io.CopyN(io.Discard, r, int64(length))
	return err
}
