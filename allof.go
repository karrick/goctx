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
	ctx context.Context
	err atomic.Pointer[error]

	// The composite pair of fields cv and count cooperate in order to act as
	// a wait-group, with the difference that it allows the Add method to
	// return an error when the AllOf has already completed.
	//
	// NOTE: sync.Cond must not be copied, so this structure allocates this
	// field seperately from AllOf in case upstream copies the AllOf value.
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
		ctx:   ctx,
		cv:    &sync.Cond{L: new(sync.Mutex)},
		count: uint64(len(contexts)), // See note below why this must be initialized.
	}

	// NOTE: Because this AllOf does not yet have any contexts added to it,
	// its count is zero. However, its public Add method returns an error when
	// adding a context when the count is zero in order to protect against
	// callers adding contexts to this Allof instance after it has
	// closed. Therefore, invoke the private add method to add all the
	// contexts provided to this at instantiation time. For this reason, the
	// private add method does not increment the count, but rather it takes
	// place in the public Add method. Which is why the instance's count field
	// is initialized to the number of contexts it is initialized with.
	for _, element := range contexts {
		go allOf.add(element)
	}

	// Spawn a goroutine that will block until all of the parent contexts have
	// closed, then after they all have, will cancel the derived context.
	go func() {
		// Identical to sync.WaitGroup.Wait(), wait until count is 0,
		// indicating there are no more contexts being monitored.
		allOf.cv.L.Lock()
		for allOf.count > 0 {
			// NOTE: When count is greater than zero, this goroutine should
			// continue waiting for more goroutines to complete.
			allOf.cv.Wait()
		}
		// POST: All monitoring goroutines have terminated.
		allOf.cv.L.Unlock()

		// Cancel the derived context using the error this AllOf instance
		// stored from when its added contexts closed.
		cancel(*allOf.err.Load())
	}()

	return allOf, nil
}

// add blocks until ctx has completed, then decrements the count of goroutines
// and their respective contexts being waited upon.
func (allOf *AllOf) add(ctx context.Context) {
	<-ctx.Done()                        // Block here until ctx has closed.
	err := context.Cause(ctx)           // Obtain the cause that the context was closed.
	allOf.err.CompareAndSwap(nil, &err) // Remember the first error.
	// allOf.err.Store(&err)				// Remember the final error.

	// NOTE: At this point, the ctx has closed, but before this method returns
	// and terminates, it must decrement the number of goroutines waiting on
	// their respective context.Context instances to close.

	// Identical to sync.WaitGroup.Done().
	allOf.cv.L.Lock()
	if allOf.count == 0 {
		panic("negative condition variable")
	}
	allOf.count--
	allOf.cv.L.Unlock()

	// NOTE: This calls Signal rather than Broadcast because there is only a
	// single goroutine that is blocked by calling Wait on the condition
	// variable, and Signal will unblock that goroutine.
	allOf.cv.Signal()
}

// Add causes AllOf to also wait until ctx has completed before it completes.
func (allOf *AllOf) Add(ctx context.Context) error {
	// This method needs this ability to prevent adding additional
	// context.Context instances to the AllOf instance after the AllOf
	// instance has already closed.
	//
	// Similar but not identical to sync.WaitGroup.Add(1), it performs the
	// same function, but after locking the condition variable's mutex, it
	// allows this AllOf struct the ability to branch on the number of
	// goroutines being waited upon before incrementing and unlocking the
	// condition variable's mutex.
	allOf.cv.L.Lock()
	if allOf.count == 0 {
		return errors.New("cannot add after AllOf complete")
	}
	allOf.count++
	allOf.cv.L.Unlock()

	// NOTE: The following Signal invocation is part of the pattern of how a
	// WaitGroup might be programmed, but because the only goroutine that is
	// blocked by calling Wait is something waiting for count to equal zero,
	// there is no point in waking it up when this knows the count is above
	// zero.
	//
	// allOf.cv.Signal()			// ??? not necessary

	// Spawn goroutine to wait for ctx to be closed, after which it will
	// decrement count.
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
