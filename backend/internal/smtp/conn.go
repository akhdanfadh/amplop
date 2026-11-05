package smtp

import (
	"bytes"
	"errors"
	"net"
	"sync/atomic"
)

// maxReplyBytes caps the bytes read from the server in one session; a test
// send needs a few kilobytes. go-smtp limits each line, not the line count.
const maxReplyBytes = 1 << 20

// errTooLong means the server sent more than maxReplyBytes.
var errTooLong = errors.New("server replies exceed the size limit")

// watchedConn wraps the server connection to cap the bytes read, to record
// whether STARTTLS was sent, and to record whether reading started after
// the message ended, which happens only once its final dot was written.
//
// NOTE: that last record relies on go-smtp's DataCommand.CloseWithResponse
// flushing the message's end and its dot before it reads the reply, which
// its source does but its documentation does not promise. SMTP forces the
// order, since no reply to the message exists before the dot. If a release
// reads earlier, TestSendFinalWriteFailed fails; if it stops reading the
// connection, TestSendUnconfirmed does. Either way a mismatch mostly errs
// toward unconfirmed, which skips a retry rather than sending twice.
type watchedConn struct {
	net.Conn
	left      int64
	startTLS  atomic.Bool
	ended     atomic.Bool // the message's end is being written
	answering atomic.Bool // a read started after ended was set
}

// Read reads from the connection until maxReplyBytes have been read in total,
// then returns errTooLong. A read after the message's end records that the
// answer is awaited.
func (c *watchedConn) Read(b []byte) (int, error) {
	if c.ended.Load() {
		c.answering.Store(true)
	}
	if c.left <= 0 {
		return 0, errTooLong
	}
	// Shorten b so a single read cannot pass the limit.
	if int64(len(b)) > c.left {
		b = b[:c.left]
	}
	n, err := c.Conn.Read(b)
	c.left -= int64(n)
	return n, err
}

// Write writes b unchanged and records whether b is the STARTTLS command.
func (c *watchedConn) Write(b []byte) (int, error) {
	// go-smtp flushes each command in one write, so the command starts b.
	if bytes.HasPrefix(b, []byte("STARTTLS\r\n")) {
		c.startTLS.Store(true)
	}
	return c.Conn.Write(b)
}
