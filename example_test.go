package goctx_test

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/karrick/goctx"
)

func ExampleAllOf() {
	ctx1, cancel1 := context.WithCancelCause(context.Background())
	ctx2, cancel2 := context.WithCancelCause(context.Background())

	// Create an AllOf instance that provides a derived context that
	// is canceled after all of its composed contexts have been
	// canceled. One or more composed contexts must be provided when
	// creating a new AllOf instance.
	allOf, err := goctx.NewAllOf(ctx1, ctx2)
	if err != nil {
		panic(err)
	}

	// From the AllOf instance obtain the derived context that will be
	// canceled after all composed contexts are canceled.
	derivedCtx := allOf.Context()

	// For example purposes, spawn a goroutine to block until the
	// derived context is canceled.
	var wg sync.WaitGroup
	wg.Add(1)
	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			// NOTE: The derived context has been canceled.
		}
		wg.Done()
	}(derivedCtx)

	// Check the number of outstanding composed contexts to be
	// canceled.
	fmt.Println("count", allOf.Count())

	// New contexts may be added to the AllOf instance even after
	// started waiting for it to complete.
	ctx3, cancel3 := context.WithCancelCause(context.Background())
	allOf.Add(ctx3)

	// Composed contexts may be canceled in any arbitrary order, and
	// can be given an error that provides the reason that context was
	// canceled.
	cancel2(errors.New("reason 2"))
	cancel3(nil)
	cancel1(errors.New("reason 1"))

	// For example purposes, wait until dervied context has canceled.
	wg.Wait()

	// Check the number of outstanding composed contexts to be
	// canceled.
	fmt.Println("count", allOf.Count())
	fmt.Println("derived error", derivedCtx.Err())

	// Because the order in which the cancellations take place are
	// non-deterministic, this test needs to check each potential
	// cause, and make sure at least one of them matches the cause
	// reported by the derived context.
	got := context.Cause(derivedCtx)

	causes := []error{
		errors.New("reason 1"),
		errors.New("reason 2"),
		errors.New("context canceled"), // cancel3 had nil cause
	}

	var found bool
	for _, want := range causes {
		if got.Error() == want.Error() {
			found = true
			break
		}
	}

	if found != true {
		fmt.Printf("GOT: %v; WANT: %v", got, causes)
	}

	// Output:
	// count 2
	// count 0
	// derived error context canceled
}
