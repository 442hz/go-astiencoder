package astilibav

import (
	"sync"

	"github.com/asticode/go-astiencoder"
	"github.com/asticode/go-astitools/stat"
	"github.com/asticode/goav/avcodec"
	"github.com/asticode/goav/avformat"
)

// PktHandler represents an object that can handle a pkt
type PktHandler interface {
	HandlePkt(p *PktHandlerPayload)
}

// PktHandlerPayload represents a PktHandler payload
type PktHandlerPayload struct {
	Pkt  *avcodec.Packet
	Prev Descriptor
}

type pktDispatcher struct {
	hs           []PktHandler
	m            *sync.Mutex
	pktPool      *sync.Pool
	statDispatch *astistat.DurationRatioStat
	wg           *sync.WaitGroup
}

func newPktDispatcher(c *astiencoder.Closer) *pktDispatcher {
	return &pktDispatcher{
		m: &sync.Mutex{},
		pktPool: &sync.Pool{New: func() interface{} {
			pkt := avcodec.AvPacketAlloc()
			c.Add(func() error {
				avcodec.AvPacketFree(pkt)
				return nil
			})
			return pkt
		}},
		statDispatch: astistat.NewDurationRatioStat(),
		wg:           &sync.WaitGroup{},
	}
}

func (d *pktDispatcher) addHandler(h PktHandler) {
	d.m.Lock()
	defer d.m.Unlock()
	d.hs = append(d.hs, h)
}

func (d *pktDispatcher) getPkt() *avcodec.Packet {
	return d.pktPool.Get().(*avcodec.Packet)
}

func (d *pktDispatcher) putPkt(pkt *avcodec.Packet) {
	pkt.AvPacketUnref()
	d.pktPool.Put(pkt)
}

func (d *pktDispatcher) dispatch(pkt *avcodec.Packet, prev Descriptor) {
	// Copy handlers
	d.m.Lock()
	var hs []PktHandler
	for _, h := range d.hs {
		v, ok := h.(PktCond)
		if !ok || v.UsePkt(pkt) {
			hs = append(hs, h)
		}
	}
	d.m.Unlock()

	// No handlers
	if len(hs) == 0 {
		return
	}

	// Wait for all previous subprocesses to be done
	// In case a brave soul tries to update this logic so that several packet can be sent to handlers in parallel, bare
	// in mind that packets must be sent in order whereas sending packets in goroutines doesn't keep this order
	d.statDispatch.Add(true)
	d.wait()
	d.statDispatch.Done(true)

	// Add subprocesses
	d.wg.Add(len(hs))

	// Loop through handlers
	for _, h := range hs {
		// Copy pkt
		hPkt := d.getPkt()
		hPkt.AvPacketRef(pkt)

		// Handle pkt
		go func(h PktHandler) {
			defer d.wg.Done()
			defer d.putPkt(hPkt)
			h.HandlePkt(&PktHandlerPayload{
				Pkt:  hPkt,
				Prev: prev,
			})
		}(h)
	}
}

func (d *pktDispatcher) wait() {
	d.wg.Wait()
}

func (d *pktDispatcher) addStats(s *astistat.Stater) {
	// Add wait time
	s.AddStat(astistat.StatMetadata{
		Description: "Percentage of time spent waiting for all previous subprocesses to be done",
		Label:       "Dispatch ratio",
		Unit:        "%",
	}, d.statDispatch)
}

// PktCond represents an object that can decide whether to use a pkt
type PktCond interface {
	UsePkt(pkt *avcodec.Packet) bool
}

type pktCond struct {
	PktHandler
	i *avformat.Stream
}

func newPktCond(i *avformat.Stream, h PktHandler) *pktCond {
	return &pktCond{
		i:          i,
		PktHandler: h,
	}
}

// UsePkt implements the PktCond interface
func (c *pktCond) UsePkt(pkt *avcodec.Packet) bool {
	return pkt.StreamIndex() == c.i.Index()
}

type pktHandlerPayloadRetriever func() *PktHandlerPayload
