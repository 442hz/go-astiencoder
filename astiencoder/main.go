package main

import (
	"encoding/json"
	"flag"
	"github.com/asticode/go-astiencoder"
	"github.com/asticode/go-astilog"
	"github.com/pkg/errors"
	"os"
)

// Flags
var (
	job = flag.String("j", "", "the path to the job in JSON format")
)

func main() {
	// Parse flags
	flag.Parse()
	astilog.FlagInit()

	// Create configuration
	c, err := newConfiguration()
	if err != nil {
		astilog.Fatal(errors.Wrap(err, "main: creating configuration failed"))
	}

	// Create worker
	w := astiencoder.NewWorker(c.Encoder)
	defer w.Close()

	// Handle signals
	w.HandleSignals()

	// Serve
	w.Serve()

	// Job has been provided
	if len(*job) > 0 {
		// Open file
		var f *os.File
		if f, err = os.Open(*job); err != nil {
			astilog.Fatal(errors.Wrapf(err, "main: opening %s failed", *job))
		}

		// Unmarshal
		var j astiencoder.Job
		if err = json.NewDecoder(f).Decode(&j); err != nil {
			astilog.Fatal(errors.Wrapf(err, "main: unmarshaling %s into %+v failed", *job, j))
		}

		// Exec job
		if err = w.Cmds().ExecJob(j); err != nil {
			astilog.Fatal(errors.Wrap(err, "main: executing job failed"))
		}
	}

	// Wait
	w.Wait()
}
