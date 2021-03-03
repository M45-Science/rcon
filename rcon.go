package rcon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Information to the protocol can be found under: https://developer.valvesoftware.com/wiki/Source_RCON_Protocol

const (
	typeAuth         = 3
	typeExecCommand  = 2
	typeRespnseValue = 0
	typeAuthResponse = 2

	fieldPackageSize = 4
	fieldIDSize      = 4
	fieldTypeSize    = 4
	fieldMinBodySize = 1
	fieldEndSize     = 1
)

// The minimum package size contains:
// 4 bytes for the ID field
// 4 bytes for the Type field
// 1 byte minimum for an empty body string
// 1 byte for the empty string at the end
//
// https://developer.valvesoftware.com/wiki/Source_RCON_Protocol#Packet_Size
// The 4 bytes representing the size of the package are not included.
const minPackageSize = fieldIDSize + fieldTypeSize + fieldMinBodySize + fieldEndSize

// maxPackageSize of a request/response package.
// https://developer.valvesoftware.com/wiki/Source_RCON_Protocol#Packet_Size
const maxPackageSize = 4096

// RemoteConsole holds the information to communicate withe remote console.
type RemoteConsole struct {
	conn      net.Conn
	readbuf   []byte
	readmu    sync.Mutex
	reqid     int32
	queuedbuf []byte
}

var (
	// ErrAuthFailed the authentication against the server failed.
	// This happens if the requeste id doesn't match the response id.
	ErrAuthFailed = errors.New("rcon: authentication failed")

	// ErrInvalidAuthResponse the response of an authentication request doesn't match the correct type.
	ErrInvalidAuthResponse = errors.New("rcon: invalid response type during auth")

	// ErrUnexpectedFormat the response package is not correctly formatted.
	ErrUnexpectedFormat = errors.New("rcon: unexpected response format")

	// ErrCommandTooLong the command is bigger than the bodyBufferSize.
	ErrCommandTooLong = errors.New("rcon: command too long")

	// ErrResponseTooLong the response package is bigger than the maxPackageSize.
	ErrResponseTooLong = errors.New("rcon: response too long")
)

// Dial establishes a connection with the remote server.
// It can return multiple errors:
// 	- ErrInvalidAuthResponse
// 	- ErrAuthFailed
// 	- and other types of connection errors that are not specified in this package.
func Dial(host, password string) (*RemoteConsole, error) {
	const timeout = 10 * time.Second
	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		return nil, err
	}

	var reqid int
	r := &RemoteConsole{conn: conn, reqid: 0x7fffffff}
	reqid, err = r.writeCmd(typeAuth, password)
	if err != nil {
		return nil, err
	}

	r.readbuf = make([]byte, maxPackageSize)

	var respType, requestID int
	respType, requestID, _, err = r.readResponse(timeout)
	if err != nil {
		return nil, err
	}

	// if we didn't get an auth response back, try again. it is often a bug
	// with RCON servers that you get an empty response before receiving the
	// auth response.
	if respType != typeAuthResponse {
		respType, requestID, _, err = r.readResponse(timeout)
	}
	if err != nil {
		return nil, err
	}
	if respType != typeAuthResponse {
		return nil, ErrInvalidAuthResponse
	}
	if requestID != reqid {
		return nil, ErrAuthFailed
	}

	return r, nil
}

// LocalAddr returns the local network address.
func (r *RemoteConsole) LocalAddr() net.Addr {
	return r.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (r *RemoteConsole) RemoteAddr() net.Addr {
	return r.conn.RemoteAddr()
}

// Write sends a command to the server.
func (r *RemoteConsole) Write(cmd string) (requestID int, err error) {
	return r.writeCmd(typeExecCommand, cmd)
}

// Read reads a incomming request from the server.
func (r *RemoteConsole) Read() (response string, requestID int, err error) {
	var respType int
	var respBytes []byte
	respType, requestID, respBytes, err = r.readResponse(2 * time.Minute)
	if err != nil || respType != typeRespnseValue {
		response = ""
		requestID = 0
	} else {
		response = string(respBytes)
	}
	return
}

// Close the connection to the server.
func (r *RemoteConsole) Close() error {
	return r.conn.Close()
}

func newRequestID(id int32) int32 {
	if id&0x0fffffff != id {
		return int32((time.Now().UnixNano() / 100000) % 100000)
	}
	return id + 1
}

func (r *RemoteConsole) writeCmd(pkgType int32, str string) (int, error) {
	if len(str) > 1024-10 {
		return -1, ErrCommandTooLong
	}

	buffer := bytes.NewBuffer(make([]byte, 0, minPackageSize+fieldPackageSize+len(str)))
	reqid := atomic.LoadInt32(&r.reqid)
	reqid = newRequestID(reqid)
	atomic.StoreInt32(&r.reqid, reqid)

	// packet size
	binary.Write(buffer, binary.LittleEndian, int32(minPackageSize+len(str)))

	// request id
	binary.Write(buffer, binary.LittleEndian, int32(reqid))

	// auth cmd
	binary.Write(buffer, binary.LittleEndian, int32(pkgType))

	// string (null terminated)
	buffer.WriteString(str)
	binary.Write(buffer, binary.LittleEndian, byte(0))

	// string 2 (null terminated)
	// we don't have a use for string 2
	binary.Write(buffer, binary.LittleEndian, byte(0))

	r.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := r.conn.Write(buffer.Bytes())
	return int(reqid), err
}

func (r *RemoteConsole) readResponse(timeout time.Duration) (int, int, []byte, error) {
	r.readmu.Lock()
	defer r.readmu.Unlock()

	r.conn.SetReadDeadline(time.Now().Add(timeout))
	var size int
	var err error
	if r.queuedbuf != nil {
		copy(r.readbuf, r.queuedbuf)
		size = len(r.queuedbuf)
		r.queuedbuf = nil
	} else {
		size, err = r.conn.Read(r.readbuf)
		if err != nil {
			return 0, 0, nil, err
		}
	}
	if size < fieldPackageSize {
		// need the 4 byte packet size...
		s, err := r.conn.Read(r.readbuf[size:])
		if err != nil {
			return 0, 0, nil, err
		}
		size += s
	}

	var dataSize32 int32
	b := bytes.NewBuffer(r.readbuf[:size])
	binary.Read(b, binary.LittleEndian, &dataSize32)
	if dataSize32 < minPackageSize {
		return 0, 0, nil, ErrUnexpectedFormat
	}

	totalSize := size
	dataSize := int(dataSize32)
	if dataSize > maxPackageSize {
		return 0, 0, nil, ErrResponseTooLong
	}

	for dataSize+4 > totalSize {
		size, err := r.conn.Read(r.readbuf[totalSize:])
		if err != nil {
			return 0, 0, nil, err
		}
		totalSize += size
	}

	data := r.readbuf[4 : 4+dataSize]
	if totalSize > dataSize+4 {
		// start of the next buffer was at the end of this packet.
		// save it for the next read.
		r.queuedbuf = r.readbuf[4+dataSize : totalSize]
	}

	return r.readResponseData(data)
}

func (r *RemoteConsole) readResponseData(data []byte) (int, int, []byte, error) {
	var requestID, responseType int32
	var response []byte
	buffer := bytes.NewBuffer(data)
	binary.Read(buffer, binary.LittleEndian, &requestID)
	binary.Read(buffer, binary.LittleEndian, &responseType)
	response, err := buffer.ReadBytes(byte(0))
	if err != nil && err != io.EOF {
		return 0, 0, nil, err
	}
	if err == nil {
		// if we didn't hit EOF, we have a null byte to remove
		response = response[:len(response)-1]
	}
	return int(responseType), int(requestID), response, nil
}
