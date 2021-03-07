package rcon

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func startTestServer(fn func(net.Conn, *bytes.Buffer)) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, maxPackageSize)
		_, err = conn.Read(buf)
		if err != nil {
			return
		}

		var packetSize, requestID, cmdType int32
		var str []byte
		b := bytes.NewBuffer(buf)
		binary.Read(b, binary.LittleEndian, &packetSize)
		binary.Read(b, binary.LittleEndian, &requestID)
		binary.Read(b, binary.LittleEndian, &cmdType)
		str, err = b.ReadBytes(0x00)
		if err != nil {
			return
		}
		if string(str[:len(str)-1]) != "blerg" {
			requestID = -1
		}

		conn.Write(buildPackage(requestID, typeAuthResponse, []byte{}).Bytes())

		if fn != nil {
			b.Reset()
			fn(conn, b)
		}
	}()

	return listener.Addr().String(), nil
}

func buildStartOfPackage(size int, requestID, responseType int32, body []byte) *bytes.Buffer {
	b := bytes.NewBuffer([]byte{})
	binary.Write(b, binary.LittleEndian, int32(minPackageSize+size))
	binary.Write(b, binary.LittleEndian, int32(requestID))
	binary.Write(b, binary.LittleEndian, int32(responseType))
	binary.Write(b, binary.LittleEndian, body)

	return b
}

func buildEndOfPackage(body []byte) *bytes.Buffer {
	b := bytes.NewBuffer([]byte{})
	binary.Write(b, binary.LittleEndian, body)
	binary.Write(b, binary.LittleEndian, byte(0))
	binary.Write(b, binary.LittleEndian, byte(0))

	return b
}

func buildPackage(requestID, responseType int32, body []byte) *bytes.Buffer {
	b := bytes.NewBuffer([]byte{})
	b.Write(buildStartOfPackage(len(body), requestID, responseType, body).Bytes())
	b.Write(buildEndOfPackage([]byte{}).Bytes())
	return b
}

func buildPackageWithLength(requestID, responseType int32, length int) *bytes.Buffer {
	return buildPackage(requestID, responseType, writeSpace(length).Bytes())
}

func writeSpace(length int) *bytes.Buffer {
	b := bytes.NewBuffer([]byte{})
	for i := 0; i < length; i++ {
		binary.Write(b, binary.LittleEndian, byte(' '))
	}

	return b
}

func TestAuth(t *testing.T) {
	addr, err := startTestServer(nil)
	if err != nil {
		t.Fatal(err)
	}

	rc, err := Dial(addr, "blerg")
	if err != nil {
		t.Fatal(err)
	}

	err = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMultipacket(t *testing.T) {

	t.Run("write one package at once", func(t *testing.T) {
		addr, err := startTestServer(func(c net.Conn, b *bytes.Buffer) {
			b.Write(buildPackageWithLength(123, typeResponseValue, 4000).Bytes())
			c.Write(b.Bytes())
		})
		if err != nil {
			t.Fatal(err)
		}

		rc, err := Dial(addr, "blerg")
		if err != nil {
			t.Fatal(err)
		}

		str, _, err := rc.Read()
		if err != nil {
			t.Fatal(err)
		}
		if len(str) != 4000 {
			t.Fatal("response length not correct")
		}

	})

	t.Run("write one package body over multiple writes", func(t *testing.T) {
		length := 4000
		addr, err := startTestServer(func(c net.Conn, b *bytes.Buffer) {
			c.Write(buildStartOfPackage(length, 123, typeResponseValue, writeSpace(length/2).Bytes()).Bytes())

			c.Write(buildEndOfPackage(writeSpace(length / 2).Bytes()).Bytes())
		})
		if err != nil {
			t.Fatal(err)
		}

		rc, err := Dial(addr, "blerg")
		if err != nil {
			t.Fatal(err)
		}

		str, _, err := rc.Read()
		if err != nil {
			t.Fatal(err)
		}
		if len(str) != 4000 {
			t.Fatal("response length not correct")
		}

	})

	t.Run("write one package size over multiple writes", func(t *testing.T) {
		length := 2000
		addr, err := startTestServer(func(c net.Conn, b *bytes.Buffer) {
			binary.Write(b, binary.LittleEndian, int32(length+minPackageSize))
			c.Write(b.Bytes()[:len(b.Bytes())-3]) // send without the last three bytes

			b.Reset()
			c.Write(buildPackageWithLength(123, typeResponseValue, length).Bytes()[1:]) // skip first byte
		})
		if err != nil {
			t.Fatal(err)
		}

		rc, err := Dial(addr, "blerg")
		if err != nil {
			t.Fatal(err)
		}

		str, _, err := rc.Read()
		if err != nil {
			t.Fatal(err)
		}
		if len(str) != 2000 {
			t.Fatal("response length not correct")
		}

	})

}
