package daemon

import "context"

type jobGate struct{ token chan struct{} }

func newJobGate() *jobGate {
	g := &jobGate{token: make(chan struct{}, 1)}
	g.token <- struct{}{}
	return g
}
func (g *jobGate) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.token:
		if err := ctx.Err(); err != nil {
			g.release()
			return err
		}
		return nil
	}
}
func (g *jobGate) release() { g.token <- struct{}{} }
