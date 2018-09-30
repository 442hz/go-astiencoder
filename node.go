package astiencoder

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/asticode/go-astitools/stat"
	"github.com/asticode/go-astitools/worker"
)

// Node represents a node
type Node interface {
	NodeChild
	NodeDescriptor
	NodeParent
	Starter
	Stater
}

// NodeDescriptor represents an object that can describe a node
type NodeDescriptor interface {
	Metadata() NodeMetadata
}

// NodeMetadata represents node metadata
type NodeMetadata struct {
	Description string
	Label       string
	Name        string
}

// NodeChild represents an object with parent nodes
type NodeChild interface {
	AddParent(n Node)
	ParentIsStarted(m NodeMetadata)
	ParentIsStopped(m NodeMetadata)
	Parents() []Node
}

// NodeParent represents an object with child nodes
type NodeParent interface {
	AddChild(n Node)
	ChildIsStarted(m NodeMetadata)
	ChildIsStopped(m NodeMetadata)
	Children() []Node
}

// Statuses
const (
	StatusStarted = "started"
	StatusStopped = "stopped"
)

// Starter represents an object that can start/stop
type Starter interface {
	Start(ctx context.Context, t CreateTaskFunc)
	Status() string
	Stop()
}

// Stater represents an object that can return its stater
type Stater interface {
	Stater() *astistat.Stater
}

// ConnectNodes connects 2 nodes
func ConnectNodes(parent, child Node) {
	parent.AddChild(child)
	child.AddParent(parent)
}

// BaseNode represents a base node
type BaseNode struct {
	cancel          context.CancelFunc
	children        map[string]Node
	childrenStarted map[string]bool
	ctx             context.Context
	e               EmitEventFunc
	m               *sync.Mutex
	md              NodeMetadata
	oStart          *sync.Once
	oStop           *sync.Once
	parents         map[string]Node
	parentsStarted  map[string]bool
	s               *astistat.Stater
}

// NewBaseNode creates a new base node
func NewBaseNode(e EmitEventFunc, m NodeMetadata) (n *BaseNode) {
	n = &BaseNode{
		children:        make(map[string]Node),
		childrenStarted: make(map[string]bool),
		m:               &sync.Mutex{},
		e:               e,
		md:              m,
		oStart:          &sync.Once{},
		oStop:           &sync.Once{},
		parents:         make(map[string]Node),
		parentsStarted:  make(map[string]bool),
	}
	if e != nil {
		n.s = astistat.NewStater(2*time.Second, n.statsHandleFunc)
	}
	return
}

// Context returns the node context
func (n *BaseNode) Context() context.Context {
	return n.ctx
}

// CreateTaskFunc is a method that can create a task
type CreateTaskFunc func() *astiworker.Task

// BaseNodeStartFunc represents a node start func
type BaseNodeStartFunc func()

// BaseNodeExecFunc represents a node exec func
type BaseNodeExecFunc func(t *astiworker.Task)

// Status implements the Starter interface
func (n *BaseNode) Status() string {
	if n.Context() != nil && n.Context().Err() == nil {
		return StatusStarted
	} else {
		return StatusStopped
	}
}

// Start starts the node
func (n *BaseNode) Start(ctx context.Context, tc CreateTaskFunc, execFunc BaseNodeExecFunc) {
	// Make sure the node can only be started once
	n.oStart.Do(func() {
		// Check context
		if ctx.Err() != nil {
			return
		}

		// Create task
		t := tc()

		// Reset context
		n.ctx, n.cancel = context.WithCancel(ctx)

		// Reset once
		n.oStop = &sync.Once{}

		// Loop through children
		for _, c := range n.Children() {
			c.ParentIsStarted(n.md)
		}

		// Loop through parents
		for _, p := range n.Parents() {
			p.ChildIsStarted(n.md)
		}

		// Send event
		if n.e != nil {
			n.e(Event{
				Name:    EventNameNodeStarted,
				Payload: n.md.Name,
			})
		}

		// Execute the rest in a goroutine
		go func() {
			// Task is done
			defer t.Done()

			// Send event
			if n.e != nil {
				defer n.e(Event{
					Name:    EventNameNodeStopped,
					Payload: n.md.Name,
				})
			}

			// Let children and parents know the node is stopped
			defer func() {
				// Loop through children
				for _, c := range n.Children() {
					c.ParentIsStopped(n.md)
				}

				// Loop through parents
				for _, p := range n.Parents() {
					p.ChildIsStopped(n.md)
				}
			}()

			// Make sure the node is stopped properly
			defer n.Stop()

			// Handle the stater
			if n.s != nil {
				// Make sure the stater is stopped properly
				defer n.s.Stop()

				// Start stater
				n.s.Start(n.ctx)
			}

			// Exec func
			execFunc(t)
		}()
	})
}

// Stop stops the node
func (n *BaseNode) Stop() {
	// Make sure the node can only be stopped once
	n.oStop.Do(func() {
		// Cancel context
		if n.cancel != nil {
			n.cancel()
		}

		// Reset once
		n.oStart = &sync.Once{}
	})
}

// AddChild implements the NodeParent interface
func (n *BaseNode) AddChild(i Node) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.children[i.Metadata().Name]; ok {
		return
	}
	n.children[i.Metadata().Name] = i
}

// ChildIsStarted implements the NodeParent interface
func (n *BaseNode) ChildIsStarted(m NodeMetadata) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.children[m.Name]; !ok {
		return
	}
	n.childrenStarted[m.Name] = true
}

// ChildIsStopped implements the NodeParent interface
func (n *BaseNode) ChildIsStopped(m NodeMetadata) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.children[m.Name]; !ok {
		return
	}
	delete(n.childrenStarted, m.Name)
	if len(n.childrenStarted) == 0 {
		n.Stop()
	}
}

// Children implements the NodeParent interface
func (n *BaseNode) Children() (ns []Node) {
	n.m.Lock()
	defer n.m.Unlock()
	var ks []string
	for k := range n.children {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		ns = append(ns, n.children[k])
	}
	return
}

// AddParent implements the NodeChild interface
func (n *BaseNode) AddParent(i Node) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.parents[i.Metadata().Name]; ok {
		return
	}
	n.parents[i.Metadata().Name] = i
}

// ParentIsStarted implements the NodeChild interface
func (n *BaseNode) ParentIsStarted(m NodeMetadata) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.parents[m.Name]; !ok {
		return
	}
	n.parentsStarted[m.Name] = true
}

// ParentIsStopped implements the NodeChild interface
func (n *BaseNode) ParentIsStopped(m NodeMetadata) {
	n.m.Lock()
	defer n.m.Unlock()
	if _, ok := n.parents[m.Name]; !ok {
		return
	}
	delete(n.parentsStarted, m.Name)
	if len(n.parentsStarted) == 0 {
		n.Stop()
	}
}

// Parents implements the NodeChild interface
func (n *BaseNode) Parents() (ns []Node) {
	n.m.Lock()
	defer n.m.Unlock()
	var ks []string
	for k := range n.parents {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		ns = append(ns, n.parents[k])
	}
	return
}

// Metadata implements the Node interface
func (n *BaseNode) Metadata() NodeMetadata {
	return n.md
}

// Stater returns the node stater
func (n *BaseNode) Stater() *astistat.Stater {
	return n.s
}

// EventStats represents stats event
type EventStats struct {
	Name  string      `json:"name"`
	Stats []EventStat `json:"stats"`
}

// EventStat represents a stat event
type EventStat struct {
	Description string      `json:"description"`
	Label       string      `json:"label"`
	Unit        string      `json:"unit"`
	Value       interface{} `json:"value"`
}

func (n *BaseNode) statsHandleFunc(stats []astistat.Stat) {
	// No stats
	if len(stats) == 0 {
		return
	}

	// Create event
	e := EventStats{
		Name:  n.md.Name,
		Stats: []EventStat{},
	}

	// Loop through stats
	for _, s := range stats {
		e.Stats = append(e.Stats, EventStat{
			Description: s.Description,
			Label:       s.Label,
			Unit:        s.Unit,
			Value:       s.Value,
		})
	}

	// Send event
	n.e(Event{
		Name:    EventNameStats,
		Payload: e,
	})
}
