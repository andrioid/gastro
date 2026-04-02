package demo

import "sync/atomic"

var count atomic.Int64

func Increment() int64 {
	return count.Add(1)
}

func Count() int {
	return int(count.Load())
}
