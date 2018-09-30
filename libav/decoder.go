package astilibav

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/asticode/go-astiencoder"
	"github.com/asticode/go-astitools/stat"
	"github.com/asticode/go-astitools/sync"
	"github.com/asticode/go-astitools/worker"
	"github.com/asticode/goav/avcodec"
	"github.com/asticode/goav/avutil"
	"github.com/pkg/errors"
)

var countDecoder uint64

// Decoder represents an object capable of decoding packets
type Decoder struct {
	*astiencoder.BaseNode
	ctxCodec         *avcodec.Context
	d                *frameDispatcher
	e                astiencoder.EmitEventFunc
	q                *astisync.CtxQueue
	r                *astisync.Regulator
	statIncomingRate *astistat.IncrementStat
	statWorkRatio    *astistat.DurationRatioStat
}

// NewDecoder creates a new decoder
func NewDecoder(ctxCodec *avcodec.Context, e astiencoder.EmitEventFunc, c *astiencoder.Closer, packetsBufferLength int) (d *Decoder) {
	count := atomic.AddUint64(&countDecoder, uint64(1))
	d = &Decoder{
		BaseNode: astiencoder.NewBaseNode(e, astiencoder.NodeMetadata{
			Description: "Decodes",
			Label:       fmt.Sprintf("Decoder #%d", count),
			Name:        fmt.Sprintf("decoder_%d", count),
		}),
		ctxCodec:         ctxCodec,
		d:                newFrameDispatcher(c, e),
		e:                e,
		q:                astisync.NewCtxQueue(),
		r:                astisync.NewRegulator(packetsBufferLength),
		statIncomingRate: astistat.NewIncrementStat(),
		statWorkRatio:    astistat.NewDurationRatioStat(),
	}
	d.addStats()
	return
}

// NewDecoderFromCodecParams creates a new decoder from codec params
func NewDecoderFromCodecParams(codecParams *avcodec.CodecParameters, e astiencoder.EmitEventFunc, c *astiencoder.Closer, packetsBufferLength int) (d *Decoder, err error) {
	// Find decoder
	var cdc *avcodec.Codec
	if cdc = avcodec.AvcodecFindDecoder(codecParams.CodecId()); cdc == nil {
		err = fmt.Errorf("astilibav: no decoder found for codec id %+v", codecParams.CodecId())
		return
	}

	// Alloc context
	var ctxCodec *avcodec.Context
	if ctxCodec = cdc.AvcodecAllocContext3(); ctxCodec == nil {
		err = fmt.Errorf("astilibav: no context allocated for codec %+v", cdc)
		return
	}

	// Copy codec parameters
	if ret := avcodec.AvcodecParametersToContext(ctxCodec, codecParams); ret < 0 {
		err = errors.Wrapf(newAvError(ret), "astilibav: avcodec.AvcodecParametersToContext on ctx %+v and codec params %+v failed", ctxCodec, codecParams)
		return
	}

	// Open codec
	if ret := ctxCodec.AvcodecOpen2(cdc, nil); ret < 0 {
		err = errors.Wrapf(newAvError(ret), "astilibav: d.ctxCodec.AvcodecOpen2 on ctx %+v and codec %+v failed", ctxCodec, cdc)
		return
	}

	// Make sure the codec is closed
	c.Add(func() error {
		if ret := ctxCodec.AvcodecClose(); ret < 0 {
			emitAvError(e, ret, "d.ctxCodec.AvcodecClose on %+v failed", ctxCodec)
		}
		return nil
	})

	// Create decoder
	d = NewDecoder(ctxCodec, e, c, packetsBufferLength)
	return
}

func (d *Decoder) addStats() {
	// Add incoming rate
	d.Stater().AddStat(astistat.StatMetadata{
		Description: "Number of packets coming in the decoder per second",
		Label:       "Incoming rate",
		Unit:        "pps",
	}, d.statIncomingRate)

	// Add work ratio
	d.Stater().AddStat(astistat.StatMetadata{
		Description: "Percentage of time spent doing some actual work",
		Label:       "Work ratio",
		Unit:        "%",
	}, d.statWorkRatio)

	// Add dispatcher stats
	d.d.addStats(d.Stater())

	// Add queue stats
	d.q.AddStats(d.Stater())
}

// Connect connects the decoder to a FrameHandler
func (d *Decoder) Connect(h FrameHandler) {
	// Add handler
	d.d.addHandler(h)

	// Connect nodes
	astiencoder.ConnectNodes(d, h.(astiencoder.Node))
}

// Start starts the decoder
func (d *Decoder) Start(ctx context.Context, t astiencoder.CreateTaskFunc) {
	d.BaseNode.Start(ctx, t, func(t *astiworker.Task) {
		// Handle context
		go d.q.HandleCtx(d.Context())

		// Set up regulator
		d.r.HandleCtx(d.Context())
		defer d.r.Wait()

		// Make sure to stop the queue properly
		defer d.q.Stop()

		// Start queue
		d.q.Start(func(p interface{}) {
			// Handle pause
			defer d.HandlePause()

			// Assert payload
			pkt := p.(*avcodec.Packet)

			// Increment incoming rate
			d.statIncomingRate.Add(1)

			// Send pkt to decoder
			d.statWorkRatio.Add(true)
			if ret := avcodec.AvcodecSendPacket(d.ctxCodec, pkt); ret < 0 {
				d.statWorkRatio.Done(true)
				emitAvError(d.e, ret, "avcodec.AvcodecSendPacket failed")
				return
			}
			d.statWorkRatio.Done(true)

			// Loop
			for {
				// Receive frame
				d.statWorkRatio.Add(true)
				if ret := avcodec.AvcodecReceiveFrame(d.ctxCodec, d.d.f); ret < 0 {
					d.statWorkRatio.Done(true)
					if ret != avutil.AVERROR_EOF && ret != avutil.AVERROR_EAGAIN {
						emitAvError(d.e, ret, "avcodec.AvcodecReceiveFrame failed")
					}
					return
				}
				d.statWorkRatio.Done(true)

				// Dispatch frame
				d.d.dispatch(d.r)
			}
		})
	})
}

// HandlePkt implements the PktHandler interface
func (d *Decoder) HandlePkt(pkt *avcodec.Packet) {
	d.q.Send(pkt, true)
}
