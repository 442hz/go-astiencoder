package main

import (
	"sync"

	"github.com/asticode/go-astiencoder"
	"github.com/asticode/go-astitools/worker"
)

type encoder struct {
	c         *ConfigurationEncoder
	ee        *astiencoder.EventEmitter
	m         *sync.Mutex
	w         *astiworker.Worker
	wp        *astiencoder.WorkflowPool
	wsStarted map[string]bool
}

func newEncoder(c *ConfigurationEncoder, ee *astiencoder.EventEmitter, wp *astiencoder.WorkflowPool) (e *encoder) {
	e = &encoder{
		c:         c,
		ee:        ee,
		m:         &sync.Mutex{},
		w:         astiworker.NewWorker(),
		wp:        wp,
		wsStarted: make(map[string]bool),
	}
	e.ee.AddHandler(astiencoder.EventHandlerOptions{Handler: e.handleEvents})
	return
}

func (e *encoder) handleEvents(evt astiencoder.Event) {
	switch evt.Name {
	case astiencoder.EventNameWorkflowStarted:
		e.m.Lock()
		defer e.m.Unlock()
		if _, err := e.wp.Workflow(evt.Payload.(string)); err != nil {
			return
		}
		e.wsStarted[evt.Payload.(string)] = true
	case astiencoder.EventNameWorkflowStopped:
		e.m.Lock()
		defer e.m.Unlock()
		delete(e.wsStarted, evt.Payload.(string))
		if e.c.Exec.StopWhenWorkflowsAreStopped && len(e.wsStarted) == 0 {
			e.w.Stop()
		}
	}
}
