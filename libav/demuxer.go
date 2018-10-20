package astilibav

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/asticode/go-astiencoder"
	"github.com/asticode/go-astitools/stat"
	"github.com/asticode/go-astitools/worker"
	"github.com/asticode/goav/avformat"
	"github.com/asticode/goav/avutil"
)

var countDemuxer uint64

// Demuxer represents an object capable of demuxing packets out of an input
type Demuxer struct {
	*astiencoder.BaseNode
	ctxFormat     *avformat.Context
	d             *pktDispatcher
	e             *astiencoder.EventEmitter
	ss            map[int]*avformat.Stream
	statWorkRatio *astistat.DurationRatioStat
}

// NewDemuxer creates a new demuxer
func NewDemuxer(ctxFormat *avformat.Context, e *astiencoder.EventEmitter, c *astiencoder.Closer) (d *Demuxer) {
	// Create demuxer
	count := atomic.AddUint64(&countDemuxer, uint64(1))
	d = &Demuxer{
		BaseNode: astiencoder.NewBaseNode(e, astiencoder.NodeMetadata{
			Description: fmt.Sprintf("Demuxes %s", ctxFormat.Filename()),
			Label:       fmt.Sprintf("Demuxer #%d", count),
			Name:        fmt.Sprintf("demuxer_%d", count),
		}),
		ctxFormat:     ctxFormat,
		d:             newPktDispatcher(c),
		e:             e,
		ss:            make(map[int]*avformat.Stream),
		statWorkRatio: astistat.NewDurationRatioStat(),
	}

	// Index streams
	for _, s := range ctxFormat.Streams() {
		d.ss[s.Index()] = s
	}

	// Add stats
	d.addStats()
	return
}

func (d *Demuxer) addStats() {
	// Add work ratio
	d.Stater().AddStat(astistat.StatMetadata{
		Description: "Percentage of time spent doing some actual work",
		Label:       "Work ratio",
		Unit:        "%",
	}, d.statWorkRatio)

	// Add dispatcher stats
	d.d.addStats(d.Stater())
}

// Connect implements the PktHandlerConnector interface
func (d *Demuxer) Connect(h PktHandler) {
	// Add handler
	d.d.addHandler(h)

	// Connect nodes
	astiencoder.ConnectNodes(d, h.(astiencoder.Node))
}

// ConnectForStream connects the demuxer to a PktHandler for a specific stream
func (d *Demuxer) ConnectForStream(h PktHandler, i *avformat.Stream) {
	// Add handler
	d.d.addHandler(newPktCond(i, h))

	// Connect nodes
	astiencoder.ConnectNodes(d, h.(astiencoder.Node))
}

// Start starts the demuxer
func (d *Demuxer) Start(ctx context.Context, t astiencoder.CreateTaskFunc) {
	d.BaseNode.Start(ctx, t, func(t *astiworker.Task) {
		// Make sure to wait for all dispatcher subprocesses to be done so that they are properly closed
		defer d.d.wait()

		// Loop
		for {
			// Read frame
			if stop := d.readFrame(); stop {
				return
			}

			// Handle pause
			d.HandlePause()

			// Check context
			if d.Context().Err() != nil {
				return
			}
		}
	})
}

func (d *Demuxer) readFrame() (stop bool) {
	// Get pkt from pool
	pkt := d.d.getPkt()
	defer d.d.putPkt(pkt)

	// Read frame
	d.statWorkRatio.Add(true)
	if ret := d.ctxFormat.AvReadFrame(pkt); ret < 0 {
		d.statWorkRatio.Done(true)
		if ret != avutil.AVERROR_EOF {
			emitAvError(d.e, ret, "ctxFormat.AvReadFrame on %s failed", d.ctxFormat.Filename())
		}
		stop = true
		return
	}
	d.statWorkRatio.Done(true)

	// Get stream
	s, ok := d.ss[pkt.StreamIndex()]
	if !ok {
		return
	}

	// Dispatch pkt
	d.d.dispatch(pkt, s)
	return
}
