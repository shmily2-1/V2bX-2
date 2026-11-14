package rate

import (
	"github.com/juju/ratelimit"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"net"
)

type Writer struct {
	writer  buf.Writer
	limiter *ratelimit.Bucket
	current func() (*ratelimit.Bucket, bool)
}

func NewDynamicRateLimitWriter(writer buf.Writer, current func() (*ratelimit.Bucket, bool)) buf.Writer {
	return &Writer{writer: writer, current: current}
}

func NewRateLimitWriter(writer buf.Writer, limiter *ratelimit.Bucket) buf.Writer {
	return &Writer{
		writer:  writer,
		limiter: limiter,
	}
}

func (w *Writer) Close() error {
	return common.Close(w.writer)
}

func (w *Writer) WriteMultiBuffer(mb buf.MultiBuffer) error {
	bucket := w.limiter
	if w.current != nil {
		var reject bool
		bucket, reject = w.current()
		if reject {
			buf.ReleaseMulti(mb)
			_ = w.Close()
			return net.ErrClosed
		}
	}
	if bucket != nil {
		bucket.Wait(int64(mb.Len()))
	}
	return w.writer.WriteMultiBuffer(mb)
}
