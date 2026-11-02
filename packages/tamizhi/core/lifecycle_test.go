package core

import (
	cordis "github.com/tr1v3r/cordis-go"
	"sync/atomic"
	"testing"
)

func TestCordisDependencyReloadAndCleanup(t *testing.T) {
	root := cordis.New()
	defer root.Fiber().Dispose()
	var starts, stops atomic.Int64
	consumer := cordis.Define[struct{}]("consumer", func(ctx *cordis.Context, _ struct{}) error {
		starts.Add(1)
		ctx.OnDispose(func() { stops.Add(1) })
		return nil
	}).WithInject("test-service")
	dependent, err := root.Load(consumer, struct{}{})
	if err != nil || dependent.State() != cordis.StatePending {
		t.Fatalf("pending dependency: %v %v", dependent.State(), err)
	}
	service := cordis.Define[struct{}]("provider", func(ctx *cordis.Context, _ struct{}) error {
		_, err := ctx.Provide("test-service", "ready")
		return err
	})
	provider, err := root.Load(service, struct{}{})
	if err != nil || dependent.State() != cordis.StateActive {
		t.Fatalf("activation: %v %v", dependent.State(), err)
	}
	provider.Dispose()
	if dependent.State() != cordis.StatePending || stops.Load() != 1 {
		t.Fatal("provider unload did not clean up its dependent")
	}
	replacement, err := root.Load(service, struct{}{})
	if err != nil || starts.Load() != 2 {
		t.Fatal("dependency replacement did not restart consumer")
	}
	replacement.Dispose()
	if stops.Load() != 2 {
		t.Fatal("replacement cleanup did not run")
	}
}
