package astiencoder

import (
	"sync"

	"sync/atomic"

	"github.com/asticode/go-astitools/error"
)

// CloseFuncAdder represents an object that can add a close func
type CloseFuncAdder interface {
	Add(f CloseFunc)
}

// CloseFunc is a method that closes something
type CloseFunc func() error

// Closer is an object that can close things
type Closer struct {
	closed uint32
	fs     []CloseFunc
	m      *sync.Mutex
}

// NewCloser creates a new closer
func NewCloser() *Closer {
	return &Closer{
		m: &sync.Mutex{},
	}
}

// Close implements the io.Closer interface
func (c *Closer) Close() (err error) {
	// Check if not already closed
	if closed := atomic.SwapUint32(&c.closed, 1); closed == 1 {
		return nil
	}

	// Get close funcs
	c.m.Lock()
	fs := append([]CloseFunc{}, c.fs...)
	c.m.Unlock()

	// Loop through closers
	var errs []error
	for _, f := range fs {
		if errC := f(); errC != nil {
			errs = append(errs, errC)
		}
	}

	// Process errors
	if len(errs) == 1 {
		err = errs[0]
	} else if len(errs) > 1 {
		err = astierror.NewMultiple(errs)
	}
	return
}

// Add adds a close func at the beginning of the list
func (c *Closer) Add(f CloseFunc) {
	c.m.Lock()
	defer c.m.Unlock()
	c.fs = append([]CloseFunc{f}, c.fs...)
}

// NewChild creates a new child closer
func (c *Closer) NewChild() (child *Closer) {
	child = NewCloser()
	c.Add(child.Close)
	return
}
