package rate

import (
	"net"

	"github.com/juju/ratelimit"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type PacketConn struct {
	N.PacketConn
	current func() (*ratelimit.Bucket, bool)
}

func NewPacketRateLimiter(c N.PacketConn, current func() (*ratelimit.Bucket, bool)) N.PacketConn {
	return &PacketConn{PacketConn: c, current: current}
}

func (c *PacketConn) FrontHeadroom() int { return N.CalculateFrontHeadroom(c.PacketConn) }
func (c *PacketConn) RearHeadroom() int  { return N.CalculateRearHeadroom(c.PacketConn) }

func (c *PacketConn) wait(n int) error {
	bucket, reject := c.current()
	if reject {
		c.PacketConn.Close()
		return net.ErrClosed
	}
	if bucket != nil {
		bucket.Wait(int64(n))
	}
	return nil
}

func (c *PacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	address, err := c.PacketConn.ReadPacket(buffer)
	if err != nil {
		return address, err
	}
	return address, c.wait(buffer.Len())
}

func (c *PacketConn) WritePacket(buffer *buf.Buffer, address M.Socksaddr) error {
	if err := c.wait(buffer.Len()); err != nil {
		buffer.Release()
		return err
	}
	return c.PacketConn.WritePacket(buffer, address)
}
