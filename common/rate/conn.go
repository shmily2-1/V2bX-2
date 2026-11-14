package rate

import (
	"net"

	"github.com/juju/ratelimit"
)

func NewConnRateLimiter(c net.Conn, l *ratelimit.Bucket) *Conn {
	return &Conn{
		Conn:    c,
		limiter: l,
	}
}

type Conn struct {
	net.Conn
	limiter *ratelimit.Bucket
	current func() (*ratelimit.Bucket, bool)
}

func NewDynamicConnRateLimiter(c net.Conn, current func() (*ratelimit.Bucket, bool)) *Conn {
	return &Conn{Conn: c, current: current}
}

func (c *Conn) wait(n int) error {
	bucket := c.limiter
	if c.current != nil {
		var reject bool
		bucket, reject = c.current()
		if reject {
			c.Conn.Close()
			return net.ErrClosed
		}
	}
	if bucket != nil && n > 0 {
		bucket.Wait(int64(n))
	}
	return nil
}

func (c *Conn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		if limitErr := c.wait(n); limitErr != nil {
			return n, limitErr
		}
	}
	return n, err
}

func (c *Conn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if n > 0 {
		if limitErr := c.wait(n); limitErr != nil {
			return n, limitErr
		}
	}
	return n, err
}
