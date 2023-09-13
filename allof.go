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
	derivedCtx    context.Context
	derivedCancel func(error)
	causeOfCancel chan error
	finished      chan struct{}
	cause         atomic.Pointer[error]

	count     uint64
	countLock sync.Mutex
}

// NewAllOf returns an AllOf instance that has a context.Context that is
// canceled only after all of the added context.Context instances have been
// canceled.
//
//	func ExampleAllOf() {
//	    ctx1, cancel1 := context.WithCancelCause(context.Background())
//	    ctx2, cancel2 := context.WithCancelCause(context.Background())
//
//	    // Create an AllOf instance that provides a derived context that
//	    // is canceled after all of its composed contexts have been
//	    // canceled. One or more composed contexts must be provided when
//	    // creating a new AllOf instance.
//	    allOf, err := goctx.NewAllOf(ctx1, ctx2)
//	    if err != nil {
//	        panic(err)
//	    }
//
//	    // From the AllOf instance obtain the derived context that will be
//	    // canceled after all composed contexts are canceled.
//	    derivedCtx := allOf.Context()
//
//	    // For example purposes, spawn a goroutine to block until the
//	    // derived context is canceled.
//	    var wg sync.WaitGroup
//	    wg.Add(1)
//	    go func(ctx context.Context) {
//	        select {
//	        case <-ctx.Done():
//	            // NOTE: The derived context has been canceled.
//	        }
//	        wg.Done()
//	    }(derivedCtx)
//
//	    // Check the number of outstanding composed contexts to be
//	    // canceled.
//	    fmt.Println("count", allOf.Count())
//
//	    // New contexts may be added to the AllOf instance even after
//	    // started waiting for it to complete.
//	    ctx3, cancel3 := context.WithCancelCause(context.Background())
//	    allOf.Add(ctx3)
//
//	    // Composed contexts may be canceled in any arbitrary order, and
//	    // can be given an error that provides the reason that context was
//	    // canceled.
//	    cancel2(errors.New("reason 2"))
//	    cancel3(nil)
//	    cancel1(errors.New("reason 1"))
//
//	    // For example purposes, wait until dervied context has canceled.
//	    wg.Wait()
//
//	    // Check the number of outstanding composed contexts to be
//	    // canceled.
//	    fmt.Println("count", allOf.Count())
//	    fmt.Println("derived error", derivedCtx.Err())
//
//	    // Because the order in which the cancellations take place are
//	    // non-deterministic, this test needs to check each potential
//	    // cause, and make sure at least one of them matches the cause
//	    // reported by the derived context.
//	    got := context.Cause(derivedCtx)
//
//	    causes := []error{
//	        errors.New("reason 1"),
//	        errors.New("reason 2"),
//	        errors.New("context canceled"), // cancel3 had nil cause
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

	derivedCtx, derivedCancel := context.WithCancelCause(context.Background())

	allOf := &AllOf{
		derivedCancel: derivedCancel,
		causeOfCancel: make(chan error),
		count:         uint64(len(contexts)), // See block note below why this must be initialized.
		derivedCtx:    derivedCtx,
		finished:      make(chan struct{}),
	}

	// NOTE: Because this AllOf does not yet have any contexts added to it,
	// its count is zero. However, its public Add method returns an error when
	// adding a context when the count is zero in order to protect against
	// callers adding contexts to this Allof instance after it has
	// canceled. Therefore, invoke the private add method to add all the
	// contexts provided to this at instantiation time. For this reason, the
	// private add method does not increment the count, but rather it takes
	// place in the public Add method. Which is why the instance's count field
	// is initialized to the number of contexts it is initialized with.
	for _, ctx := range contexts {
		go allOf.add(ctx)
	}

	return allOf, nil
}

// add blocks until ctx has completed, then decrements the count of goroutines
// and their respective contexts being waited upon.
func (allOf *AllOf) add(ctx context.Context) {
	// Block until either ctx or the causeOfCancel channel canceled.
	select {
	case <-ctx.Done():
		// When a context.Context is canceled, it stores the first error and
		// cause because that is the error and cause that caused it to be
		// canceled. However, the AllOf derived context is not canceled until
		// all of its source contexts have been canceled, or in other words,
		// its final source context is canceled. Therefore, rather than
		// remembering the first error and cause, this remembers the error and
		// cause of the final source context by overwriting whatever was
		// previously stored with the cause of the most recent cancellation.
		cause := context.Cause(ctx)
		allOf.cause.Store(&cause)
		debug("Upstream context canceled: %v\n", cause)
	case cause, ok := <-allOf.causeOfCancel:
		if ok {
			// The first time this branch is used is when the AllOf instance
			// is canceled and a non-nil cause was provided. Store that
			// cause. After the channel is canceled, however, all of the other
			// goroutines will enter this branch, but not be given a cause.
			allOf.cause.Store(&cause)
			debug("Do have cause of cancel ok: %v\n", cause)
		} else {
			debug("Do not have cause of cancel\n")
		}
	}

	// NOTE: Before this method returns and terminates, it must decrement the
	// number of goroutines waiting on their respective context.Context
	// instances to close.
	allOf.countLock.Lock()

	if allOf.count == 0 {
		// While the count is not currently negative, the next operation is to
		// decrement it, causing it to be negative if it were a signed
		// integer.
		allOf.countLock.Unlock()
		panic("negative goroutine counter")
	}

	// Decrement counter when one fewer goroutines are awaiting completion.
	allOf.count--

	if allOf.count > 0 {
		allOf.countLock.Unlock()
		debug("There are one or more goroutines are awaiting completion.\n")
		return
	}

	allOf.countLock.Unlock()

	// POST: count equals 0, which means that this goroutine was the final
	// goroutine waiting on its context to be canceled.
	debug("There are no more goroutines awaiting context completion.\n")

	// Cancel the derived context using the error this AllOf instance stored
	// from when its added contexts canceled.
	cause := allOf.cause.Load()
	allOf.derivedCancel(*cause)
	debug("I have a cause: %v\n", *cause)

	close(allOf.finished)
}

// Add causes AllOf to also wait until ctx has completed before it completes.
func (allOf *AllOf) Add(ctx context.Context) error {
	// This method needs this ability to prevent adding additional
	// context.Context instances to the AllOf instance after the AllOf
	// instance has already canceled.
	allOf.countLock.Lock()
	if allOf.count == 0 {
		return errors.New("cannot add after canceled")
	}
	allOf.count++
	allOf.countLock.Unlock()

	// Spawn goroutine to wait for ctx to be canceled, after which it will
	// decrement count.
	go allOf.add(ctx)

	return nil
}

// Cancel is used when the derived context is no longer required, and used to
// terminate all goroutines spawned by this AllOf instance that track the
// cancellation of their respective context.Context instances, and then
// cancels the derived context. This method does not return until all spawned
// goroutines have been terminated.
//
// NOTE: This method does not in any way cancel or otherwise affect the
// context.Context instances passed to this instance's Add method. It only
// cancels the derived context and stops the goroutines watching the added
// contexts.
func (allOf *AllOf) Cancel(cause error) {
	allOf.causeOfCancel <- cause
	close(allOf.causeOfCancel)
	<-allOf.finished
}

// Context returns a derived context.Context that will be canceled only after
// all of the added contexts have been canceled.
func (allOf *AllOf) Context() context.Context { return allOf.derivedCtx }

// Count returns the number of added contexts still waiting for closure.
func (allOf *AllOf) Count() int {
	allOf.countLock.Lock()
	count := int(allOf.count)
	allOf.countLock.Unlock()
	return count
}
