package astiencoder

import "context"

func CtxFunc(ctx context.Context, fn func() error) (err error) {
	if err = fn(); err != nil {
		return err
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	return
}
