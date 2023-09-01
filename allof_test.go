package goctx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

func ExampleAllOf() {
	ctx1, cancel1 := context.WithCancelCause(context.Background())
	ctx2, cancel2 := context.WithCancelCause(context.Background())

	// Create an AllOf instance that provides a derived context that is closed
	// after all of its composed contexts have been closed. One or more
	// composed contexts must be provided when creating a new AllOf instance.
	allOf, err := NewAllOf(ctx1, ctx2)
	if err != nil {
		panic(err)
	}

	// From the AllOf instance obtain the derived context that will be closed
	// after all composed contexts are closed.
	derivedCtx := allOf.Context()

	// For example purposes, spawn a goroutine to block until the derived
	// context is closed.
	var wg sync.WaitGroup
	wg.Add(1)
	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			// NOTE: The derived context has been closed.
		}
		wg.Done()
	}(derivedCtx)

	// Check the number of outstanding composed contexts to be closed.
	fmt.Println("count", allOf.Count())

	// New contexts may be added to the AllOf instance even after started
	// waiting for it to complete.
	ctx3, cancel3 := context.WithCancelCause(context.Background())
	allOf.Add(ctx3)

	// Composed contexts may be closed in any arbitrary order, and can be
	// given an error that provides the reason that context was closed.
	cancel2(errors.New("reason 2"))
	cancel3(nil)
	cancel1(errors.New("reason 1"))

	// For example purposes, wait until dervied context has closed.
	wg.Wait()

	// Check the number of outstanding composed contexts to be closed.
	fmt.Println("count", allOf.Count())
	fmt.Println("derived error", derivedCtx.Err())

	// Because the order in which the cancellations take place are
	// non-deterministic, this test needs to check each potential cause, and
	// make sure at least one of them matches the cause reported by the
	// derived context.
	got := context.Cause(derivedCtx)

	causes := []error{
		errors.New("reason 1"),
		errors.New("reason 2"),
		errors.New("context canceled"), // cancel3 was invoked with nil cause
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

func TestAllOf(t *testing.T) {
	t.Run("NewAllOf", func(t *testing.T) {
		t.Run("no contexts", func(t *testing.T) {
			_, err := NewAllOf()
			ensureError(t, err, "cannot create")
		})

		t.Run("one context", func(t *testing.T) {
			ctx1, cancel1 := context.WithCancelCause(context.Background())

			allOf, err := NewAllOf(ctx1)
			ensureError(t, err)

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				ctx := allOf.Context()
				select {
				case <-ctx.Done():
					// Ensure original context has error and cause.
					ensureError(t, ctx1.Err(), context.Canceled.Error())
					ensureError(t, context.Cause(ctx1), io.EOF.Error())

					// Ensure derived context has error and cause.
					ensureError(t, ctx.Err(), context.Canceled.Error())
					ensureError(t, context.Cause(ctx), io.EOF.Error())
				}
				wg.Done()
			}()

			cancel1(io.EOF)

			wg.Wait()
		})
	})

	t.Run("Add returns error after complete", func(t *testing.T) {
		ctx1, cancel1 := context.WithCancelCause(context.Background())

		allOf, err := NewAllOf(ctx1)
		ensureError(t, err)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			ctx := allOf.Context()
			select {
			case <-ctx.Done():
				// Ensure original context has error and cause.
				ensureError(t, ctx1.Err(), context.Canceled.Error())
				ensureError(t, context.Cause(ctx1), io.EOF.Error())

				// Ensure derived context has error and cause.
				ensureError(t, ctx.Err(), context.Canceled.Error())
				ensureError(t, context.Cause(ctx), io.EOF.Error())
			}
			wg.Done()
		}()

		cancel1(io.EOF)

		wg.Wait()

		ensureError(t, allOf.Add(context.Background()), "cannot add after")
	})

	t.Run("waits for all watchers", func(t *testing.T) {
		ctx1, cancel1 := context.WithCancelCause(context.Background())
		ctx2, cancel2 := context.WithCancelCause(context.Background())
		ctx3, cancel3 := context.WithCancelCause(context.Background())
		ctx4, cancel4 := context.WithCancelCause(context.Background())

		allOf, err := NewAllOf(ctx1, ctx2)
		ensureError(t, err)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			ctx := allOf.Context()
			select {
			case <-ctx.Done():
				// Ensure derived context has error and cause.
				ensureError(t, ctx.Err(), context.Canceled.Error())

				// Because the order in which the cancellations take place are
				// non-deterministic, this test needs to check each potential
				// cause, and make sure at least one of them matches the cause
				// reported by the derived context.
				got := context.Cause(ctx)

				causes := []error{
					io.EOF,
					io.ErrShortBuffer,
					io.ErrShortWrite,
					io.ErrUnexpectedEOF,
				}

				var found bool
				for _, want := range causes {
					if got == want {
						found = true
						break
					}
				}

				if found != true {
					t.Errorf("GOT: %v; WANT: %v", got, causes)
				}
			}
			wg.Done()
		}()

		cancel1(io.EOF)
		allOf.Add(ctx3)
		cancel3(io.ErrShortWrite)
		allOf.Add(ctx4)
		cancel2(io.ErrShortBuffer)
		cancel4(io.ErrUnexpectedEOF)

		wg.Wait()
	})
}