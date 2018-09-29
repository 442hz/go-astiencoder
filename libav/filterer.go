package astilibav

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/asticode/go-astiencoder"
	"github.com/asticode/go-astitools/sync"
	"github.com/asticode/go-astitools/worker"
	"github.com/asticode/goav/avcodec"
	"github.com/asticode/goav/avfilter"
	"github.com/asticode/goav/avutil"
	"github.com/pkg/errors"
)

var countFilterer uint64

// Filterer represents an object capable of applying a filter to frames
type Filterer struct {
	*astiencoder.BaseNode
	bufferSinkCtx       *avfilter.Context
	bufferSrcCtx        *avfilter.Context
	d                   *frameDispatcher
	e                   astiencoder.EmitEventFunc
	g                   *avfilter.Graph
	packetsBufferLength int
	q                   *astisync.CtxQueue
}

// NewFilterer creates a new filterer
func NewFilterer(bufferSrcCtx, bufferSinkCtx *avfilter.Context, g *avfilter.Graph, e astiencoder.EmitEventFunc, c *astiencoder.Closer, packetsBufferLength int) *Filterer {
	// Create filterer
	count := atomic.AddUint64(&countFilterer, uint64(1))
	return &Filterer{
		BaseNode: astiencoder.NewBaseNode(e, astiencoder.NodeMetadata{
			Description: "Filters",
			Label:       fmt.Sprintf("Filterer #%d", count),
			Name:        fmt.Sprintf("filterer_%d", count),
		}),
		bufferSinkCtx:       bufferSinkCtx,
		bufferSrcCtx:        bufferSrcCtx,
		d:                   newFrameDispatcher(c, e),
		e:                   e,
		g:                   g,
		packetsBufferLength: packetsBufferLength,
		q:                   astisync.NewCtxQueue(),
	}
}

// FiltererOptions represents filterer options
type FiltererOptions struct {
	Content string
	Input   FiltererInputOptions
}

// FiltererInputOptions represent filterer input options
type FiltererInputOptions struct {
	CodecType         avcodec.MediaType
	Height            int
	PixelFormat       avutil.PixelFormat
	SampleAspectRatio avutil.Rational
	TimeBase          avutil.Rational
	Width             int
}

// NewFiltererFromOptions creates a new filterer based on options
func NewFiltererFromOptions(o FiltererOptions, e astiencoder.EmitEventFunc, c *astiencoder.Closer, packetsBufferLength int) (_ *Filterer, err error) {
	// Create graph
	g := avfilter.AvfilterGraphAlloc()
	c.Add(func() error {
		g.AvfilterGraphFree()
		return nil
	})

	// Create buffers
	bufferSrc := avfilter.AvfilterGetByName("buffer")
	bufferSink := avfilter.AvfilterGetByName("buffersink")

	// Create filter in args
	var args string
	switch o.Input.CodecType {
	case avcodec.AVMEDIA_TYPE_VIDEO:
		args = fmt.Sprintf("video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=%d/%d", o.Input.Width, o.Input.Height, o.Input.PixelFormat, o.Input.TimeBase.Num(), o.Input.TimeBase.Den(), o.Input.SampleAspectRatio.Num(), o.Input.SampleAspectRatio.Den())
	default:
		err = fmt.Errorf("astilibav: codec type %d is not handled by filterer", o.Input.CodecType)
		return
	}

	// Create filter in
	var bufferSrcCtx *avfilter.Context
	if ret := avfilter.AvfilterGraphCreateFilter(&bufferSrcCtx, bufferSrc, "in", args, nil, g); ret < 0 {
		err = errors.Wrapf(newAvError(ret), "astilibav: avfilter.AvfilterGraphCreateFilter on args %s failed", args)
		return
	}

	// Create filter sink
	var bufferSinkCtx *avfilter.Context
	if ret := avfilter.AvfilterGraphCreateFilter(&bufferSinkCtx, bufferSink, "out", "", nil, g); ret < 0 {
		err = errors.Wrap(newAvError(ret), "astilibav: avfilter.AvfilterGraphCreateFilter on empty args failed")
		return
	}

	// Create outputs
	outputs := avfilter.AvfilterInoutAlloc()
	defer avfilter.AvfilterInoutFree(&outputs)
	outputs.SetName("in")
	outputs.SetFilterCtx(bufferSrcCtx)
	outputs.SetPadIdx(0)
	outputs.SetNext(nil)

	// Create inputs
	inputs := avfilter.AvfilterInoutAlloc()
	defer avfilter.AvfilterInoutFree(&inputs)
	inputs.SetName("out")
	inputs.SetFilterCtx(bufferSinkCtx)
	inputs.SetPadIdx(0)
	inputs.SetNext(nil)

	// Parse content
	if ret := g.AvfilterGraphParsePtr(o.Content, &inputs, &outputs, nil); ret < 0 {
		err = errors.Wrapf(newAvError(ret), "astilibav: g.AvfilterGraphParsePtr on content %s failed", o.Content)
		return
	}

	// Configure
	if ret := g.AvfilterGraphConfig(nil); ret < 0 {
		err = errors.Wrap(newAvError(ret), "astilibav: g.AvfilterGraphConfig failed")
		return
	}

	// Create filterer
	return NewFilterer(bufferSrcCtx, bufferSinkCtx, g, e, c, packetsBufferLength), nil
}

// Connect connects the filterer to a FrameHandler
func (f *Filterer) Connect(h FrameHandler) {
	// Append handler
	f.d.addHandler(h)

	// Connect nodes
	astiencoder.ConnectNodes(f, h.(astiencoder.Node))
}

// Start starts the filterer
func (f *Filterer) Start(ctx context.Context, t astiencoder.CreateTaskFunc) {
	f.BaseNode.Start(ctx, t, func(t *astiworker.Task) {
		// Handle context
		go f.q.HandleCtx(f.Context())

		// Create regulator
		r := astisync.NewRegulator(f.Context(), f.packetsBufferLength)
		defer r.Wait()

		// Make sure to stop the queue properly
		defer f.q.Stop()

		// Start queue
		f.q.Start(func(p interface{}) {
			// Assert payload
			fm := p.(*avutil.Frame)

			// Push frame in graph
			if ret := f.g.AvBuffersrcAddFrameFlags(f.bufferSrcCtx, fm, avfilter.AV_BUFFERSRC_FLAG_KEEP_REF); ret < 0 {
				emitAvError(f.e, ret, "f.g.AvBuffersrcAddFrameFlags failed")
				return
			}

			// Loop
			for {
				// Pull filtered frame from graph
				if ret := f.g.AvBuffersinkGetFrame(f.bufferSinkCtx, f.d.f); ret < 0 {
					if ret != avutil.AVERROR_EOF && ret != avutil.AVERROR_EAGAIN {
						emitAvError(f.e, ret, "f.g.AvBuffersinkGetFrame failed")
					}
					return
				}

				// Dispatch frame
				f.d.dispatch(r)
			}
		})
	})
}

// HandleFrame implements the FrameHandler interface
func (f *Filterer) HandleFrame(fm *avutil.Frame) {
	f.q.Send(fm, true)
}
