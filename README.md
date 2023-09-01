# goctx

## Description

Go context composition library.

## Example

```Go
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
```

## Why?

In some cases it might be useful to spawn a potentially long-running goroutine
to serve one or more clients, and you want to be able to cancel the
long-running goroutine if all of the clients have canceled their contexts, but
not before.
