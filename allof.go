package goctx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// AllOf provides a context.Context that is canceled only after all of the
// added context.Context instances have been canceled.
type AllOf struct {
	ctx  context.Context
	done chan struct{}
	err  atomic.Pointer[error]

	// The composite pair of fields cv and count cooperate in order to act as
	// a wait-group, with the difference that it allows the Add method to
	// return an error when the AllOf has already completed.
	//
	// NOTE: sync.Cond must not be copied, so allocate seperately from AllOf
	// in case upstream copies the AllOf value.
	cv    *sync.Cond
	count uint64
}

// NewAllOf returns an AllOf instance that has a context.Context that is
// canceled only after all of the added context.Context instances have been
// canceled.
//
//	func ExampleAllOf() {
//	    ctx1, cancel1 := context.WithCancelCause(context.Background())
//	    ctx2, cancel2 := context.WithCancelCause(context.Background())
//
//	    // Create an AllOf instance that provides a derived context that is closed
//	    // after all of its composed contexts have been closed. One or more
//	    // composed contexts must be provided when creating a new AllOf instance.
//	    allOf, err := NewAllOf(ctx1, ctx2)
//	    if err != nil {
//	        panic(err)
//	    }
//
//	    // From the AllOf instance obtain the derived context that will be closed
//	    // after all composed contexts are closed.
//	    derivedCtx := allOf.Context()
//
//	    // For example purposes, spawn a goroutine to block until the derived
//	    // context is closed.
//	    var wg sync.WaitGroup
//	    wg.Add(1)
//	    go func(ctx context.Context) {
//	        select {
//	        case <-ctx.Done():
//	            // NOTE: The derived context has been closed.
//	        }
//	        wg.Done()
//	    }(derivedCtx)
//
//	    // Check the number of outstanding composed contexts to be closed.
//	    fmt.Println("count", allOf.Count())
//
//	    // New contexts may be added to the AllOf instance even after started
//	    // waiting for it to complete.
//	    ctx3, cancel3 := context.WithCancelCause(context.Background())
//	    allOf.Add(ctx3)
//
//	    // Composed contexts may be closed in any arbitrary order, and can be
//	    // given an error that provides the reason that context was closed.
//	    cancel2(errors.New("reason 2"))
//	    cancel3(nil)
//	    cancel1(errors.New("reason 1"))
//
//	    // For example purposes, wait until dervied context has closed.
//	    wg.Wait()
//
//	    // Check the number of outstanding composed contexts to be closed.
//	    fmt.Println("count", allOf.Count())
//	    fmt.Println("derived error", derivedCtx.Err())
//
//	    // Because the order in which the cancellations take place are
//	    // non-deterministic, this test needs to check each potential cause, and
//	    // make sure at least one of them matches the cause reported by the
//	    // derived context.
//	    got := context.Cause(derivedCtx)
//
//	    causes := []error{
//	        errors.New("reason 1"),
//	        errors.New("reason 2"),
//	        errors.New("context canceled"), // cancel3 was invoked with nil cause
//	    }
//
//	    var found bool
//	    for _, want := range causes {
//	        if got.Error() == want.Error() {
//	            found = true
//	            break
//	        }
//	    }
//
//	    if found != true {
//	        fmt.Printf("GOT: %v; WANT: %v", got, causes)
//	    }
//
//	    // Output:
//	    // count 2
//	    // count 0
//	    // derived error context canceled
//	}
func NewAllOf(contexts ...context.Context) (*AllOf, error) {
	if len(contexts) == 0 {
		return nil, errors.New("cannot create goctx.AllOf without at least one context.Context")
	}

	ctx, cancel := context.WithCancelCause(context.Background())

	allOf := &AllOf{
		ctx:  ctx,
		done: make(chan struct{}),

		cv:    &sync.Cond{L: new(sync.Mutex)},
		count: uint64(len(contexts)),
	}

	for _, element := range contexts {
		go allOf.add(element)
	}

	go func() {
		// Wait until count is 0, indicating there are no more contexts being
		// monitored.
		allOf.cv.L.Lock()
		for allOf.count > 0 {
			allOf.cv.Wait()
		}
		allOf.cv.L.Unlock()
		// POST: All monitoring goroutines have terminated.

		// The done channel is not needed any more.
		close(allOf.done)

		// Stuff final non-nil error, or nil if no errors.
		cancel(*allOf.err.Load())
	}()

	return allOf, nil
}

// add blocks until either ctx has completed, or the instance done channel has
// been closed. After either of those conditions takes place, this method
// decrements the count of contexts being waited for by one.
func (allOf *AllOf) add(ctx context.Context) {
	select {
	case <-ctx.Done():
		if err := context.Cause(ctx); err != nil {
			allOf.err.CompareAndSwap(nil, &err)
		}
	case <-allOf.done:
		//
	}

	// allOf.wg.Done()
	allOf.cv.L.Lock()
	if allOf.count == 0 {
		panic("negative condition variable")
	}
	allOf.count--
	allOf.cv.L.Unlock()
	allOf.cv.Broadcast()
}

// Add causes AllOf to also wait until ctx has completed before it completes.
func (allOf *AllOf) Add(ctx context.Context) error {
	// allOf.wg.Add(1)
	allOf.cv.L.Lock()
	if allOf.count == 0 {
		return errors.New("cannot add after AllOf complete")
	}
	allOf.count++
	allOf.cv.L.Unlock()
	allOf.cv.Signal()

	go allOf.add(ctx)

	return nil
}

// Context returns a derived context.Context that will be closed only after
// all of the added contexts have been closed.
func (allOf *AllOf) Context() context.Context { return allOf.ctx }

// Count returns the number of added contexts still waiting for closure.
func (allOf *AllOf) Count() int {
	allOf.cv.L.Lock()
	count := int(allOf.count)
	allOf.cv.L.Unlock()
	return count
}
