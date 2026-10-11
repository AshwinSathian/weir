package memory_test

import (
	"context"
	"fmt"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/store/memory"
)

func ExampleNew() {
	st, err := memory.New(memory.Config{Shards: 8})
	if err != nil {
		fmt.Println("new:", err)
		return
	}
	// The engine does not close a store it did not create.
	defer st.Close()

	e, err := weir.New(weir.Config{Store: st})
	if err != nil {
		fmt.Println("weir:", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer e.Close(ctx)
	fmt.Println("engine uses a caller-owned store")

	_, err = memory.New(memory.Config{Shards: 3})
	fmt.Println("shards must be a power of two:", err != nil)
	// Output:
	// engine uses a caller-owned store
	// shards must be a power of two: true
}
