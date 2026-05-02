package gastro

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type testDepsA struct {
	Name string
}

type testDepsB struct {
	Count int
}

func TestAttachDeps_NilParentValueReturnsContextWithDeps(t *testing.T) {
	t.Parallel()
	deps := map[reflect.Type]any{
		reflect.TypeOf(testDepsA{}): testDepsA{Name: "alpha"},
	}
	ctx := AttachDeps(context.Background(), deps)
	got := FromContext[testDepsA](ctx)
	if got.Name != "alpha" {
		t.Fatalf("Name = %q, want %q", got.Name, "alpha")
	}
}

func TestAttachDeps_EmptyMapReturnsParent(t *testing.T) {
	t.Parallel()
	parent := context.Background()
	if got := AttachDeps(parent, nil); got != parent {
		t.Errorf("nil deps map: returned ctx differs from parent")
	}
	if got := AttachDeps(parent, map[reflect.Type]any{}); got != parent {
		t.Errorf("empty deps map: returned ctx differs from parent")
	}
}

func TestAttachDeps_ChildOverridesParent(t *testing.T) {
	t.Parallel()
	parent := AttachDeps(context.Background(), map[reflect.Type]any{
		reflect.TypeOf(testDepsA{}): testDepsA{Name: "parent"},
	})
	child := AttachDeps(parent, map[reflect.Type]any{
		reflect.TypeOf(testDepsA{}): testDepsA{Name: "child"},
	})
	if got := FromContext[testDepsA](child); got.Name != "child" {
		t.Errorf("override: got %q, want child", got.Name)
	}
	// Parent unchanged.
	if got := FromContext[testDepsA](parent); got.Name != "parent" {
		t.Errorf("parent mutated: got %q, want parent", got.Name)
	}
}

func TestAttachDeps_MergesSiblingTypes(t *testing.T) {
	t.Parallel()
	parent := AttachDeps(context.Background(), map[reflect.Type]any{
		reflect.TypeOf(testDepsA{}): testDepsA{Name: "alpha"},
	})
	child := AttachDeps(parent, map[reflect.Type]any{
		reflect.TypeOf(testDepsB{}): testDepsB{Count: 7},
	})
	if got := FromContext[testDepsA](child); got.Name != "alpha" {
		t.Errorf("A in merged ctx: got %q", got.Name)
	}
	if got := FromContext[testDepsB](child); got.Count != 7 {
		t.Errorf("B in merged ctx: got %d", got.Count)
	}
}

func TestFromContext_PanicsWhenMissing(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "no dependency of type") || !strings.Contains(msg, "WithDeps") {
			t.Errorf("panic message lacks helpful hint: %q", msg)
		}
	}()
	_ = FromContext[testDepsA](context.Background())
}

func TestFromContextOK_MissingReturnsZeroFalse(t *testing.T) {
	t.Parallel()
	v, ok := FromContextOK[testDepsA](context.Background())
	if ok {
		t.Errorf("ok = true, want false")
	}
	if v != (testDepsA{}) {
		t.Errorf("v = %#v, want zero", v)
	}
}

func TestFromContext_ConcurrentReadsAreSafe(t *testing.T) {
	t.Parallel()
	deps := map[reflect.Type]any{
		reflect.TypeOf(testDepsA{}): testDepsA{Name: "shared"},
	}
	ctx := AttachDeps(context.Background(), deps)

	const goroutines = 32
	const iters = 200
	done := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < iters; j++ {
				if got := FromContext[testDepsA](ctx); got.Name != "shared" {
					t.Errorf("got %q", got.Name)
					return
				}
			}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}
