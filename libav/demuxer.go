package astilibav

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/asticode/go-astiencoder"
	"github.com/asticode/go-astitools/stat"
	"github.com/asticode/go-astitools/sync"
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
	e             astiencoder.EmitEventFunc
	r             *astisync.Regulator
	statWorkRatio *astistat.DurationRatioStat
}

// NewDemuxer creates a new demuxer
func NewDemuxer(ctxFormat *avformat.Context, e astiencoder.EmitEventFunc, c *astiencoder.Closer, packetsBufferLength int) (d *Demuxer) {
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
		r:             astisync.NewRegulator(packetsBufferLength),
		statWorkRatio: astistat.NewDurationRatioStat(),
	}
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

// Connect connects the demuxer to a PktHandler for a specific stream index
func (d *Demuxer) Connect(i *avformat.Stream, h PktHandler) {
	// Add handler
	d.d.addHandler(newPktCond(i, h))

	// Connect nodes
	astiencoder.ConnectNodes(d, h.(astiencoder.Node))
}

// Start starts the demuxer
func (d *Demuxer) Start(ctx context.Context, t astiencoder.CreateTaskFunc) {
	d.BaseNode.Start(ctx, t, func(t *astiworker.Task) {
		// Set up regulator
		d.r.HandleCtx(d.Context())
		defer d.r.Wait()

		// Loop
		for {
			// Read frame
			d.statWorkRatio.Add(true)
			if ret := d.ctxFormat.AvReadFrame(d.d.pkt); ret < 0 {
				d.statWorkRatio.Done(true)
				if ret != avutil.AVERROR_EOF {
					emitAvError(d.e, ret, "ctxFormat.AvReadFrame on %s failed", d.ctxFormat.Filename())
				}
				return
			}
			d.statWorkRatio.Done(true)

			// Dispatch pkt
			d.d.dispatch(d.r)

			// Handle pause
			d.HandlePause()

			// Check context
			if d.Context().Err() != nil {
				return
			}
		}
	})
}
