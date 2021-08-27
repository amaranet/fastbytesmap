package fastintmap

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type Animal struct {
	name string
}

func uKey(i int) uintptr { return uintptr(i) }

func TestMapCreation(t *testing.T) {
	m := &HashMap{}
	if m.Len() != 0 {
		t.Errorf("new map should be empty but has %d items.", m.Len())
	}
}

func TestGrow(t *testing.T) {
	m := &HashMap{}
	m.Grow(uintptr(63))

	for { // make sure to wait for resize operation to finish
		if atomic.LoadUintptr(&m.resizing) == 0 {
			break
		}
		time.Sleep(time.Microsecond * 50)
	}

	d := m.mapData()
	if d.keyshifts != 58 {
		t.Error("Grow operation did not result in correct internal map data structure.")
	}
}

func TestResize(t *testing.T) {
	m := New(2)
	itemCount := 50

	for i := 0; i < itemCount; i++ {
		m.Set(uintptr(i), &Animal{strconv.Itoa(i)})
	}

	if m.Len() != itemCount {
		t.Error("Expected element count did not match.")
	}

	for { // make sure to wait for resize operation to finish
		if atomic.LoadUintptr(&m.resizing) == 0 {
			break
		}
		time.Sleep(time.Microsecond * 50)
	}

	if m.FillRate() != 0.5 {
		t.Errorf("Expecting 0.5 fill-rate got %f.", m.FillRate())
	}

	for i := 0; i < itemCount; i++ {
		_, ok := m.Get(uintptr(i))
		if !ok {
			t.Error("Getting inserted item failed.")
		}
	}
}

func TestHashedKey(t *testing.T) {
	m := &HashMap{}
	_, ok := m.Get(uintptr(0))
	if ok {
		t.Error("empty map should not return an item.")
	}
	m.DelHashedKey(uintptr(0))
	m.allocate(uintptr(64))
	m.DelHashedKey(uintptr(0))

	itemCount := 16
	log := log2(uintptr(itemCount))

	for i := 0; i < itemCount; i++ {
		m.Set(uintptr(i)<<(strconv.IntSize-log), &Animal{strconv.Itoa(i)})
	}

	if m.Len() != itemCount {
		t.Error("Expected element count did not match.")
	}

	for i := 0; i < itemCount; i++ {
		_, ok = m.Get(uintptr(i) << (strconv.IntSize - log))
		if !ok {
			t.Error("Getting inserted item failed.")
		}
	}

	for i := 0; i < itemCount; i++ {
		m.DelHashedKey(uintptr(i) << (strconv.IntSize - log))
	}
	_, ok = m.Get(uintptr(0))
	if ok {
		t.Error("item for key should not exist.")
	}
	if m.Len() != 0 {
		t.Error("Map is not empty.")
	}
}

func TestCompareAndSwapHashedKey(t *testing.T) {
	m := &HashMap{}
	elephant := &Animal{"elephant"}
	monkey := &Animal{"monkey"}

	m.Set(1<<(strconv.IntSize-2), elephant)
	if m.Len() != 1 {
		t.Error("map should contain exactly one element.")
	}
	if !m.CasHashedKey(1<<(strconv.IntSize-2), elephant, monkey) {
		t.Error("Cas should success if expectation met")
	}
	if m.Len() != 1 {
		t.Error("map should contain exactly one element.")
	}
	if m.CasHashedKey(1<<(strconv.IntSize-2), elephant, monkey) {
		t.Error("Cas should fail if expectation didn't meet")
	}
	if m.Len() != 1 {
		t.Error("map should contain exactly one element.")
	}
	item, ok := m.Get(1 << (strconv.IntSize - 2))
	if !ok {
		t.Error("ok should be true for item stored within the map.")
	}
	if item != monkey {
		t.Error("wrong item returned.")
	}
}

func TestHashMap_parallel(t *testing.T) {
	max := 10
	dur := 2 * time.Second
	m := &HashMap{}
	do := func(t *testing.T, max int, d time.Duration, fn func(*testing.T, int)) <-chan error {
		t.Helper()
		done := make(chan error)
		var times int64
		// This goroutines will terminate test in case if closure hangs.
		go func() {
			for {
				select {
				case <-time.After(d + 500*time.Millisecond):
					if atomic.LoadInt64(&times) == 0 {
						done <- fmt.Errorf("closure was not executed even once, something blocks it")
					}
					close(done)
				case <-done:
				}
			}
		}()
		go func() {
			timer := time.NewTimer(d)
			defer timer.Stop()
		InfLoop:
			for {
				for i := 0; i < max; i++ {
					select {
					case <-timer.C:
						break InfLoop
					default:
					}
					fn(t, i)
					atomic.AddInt64(&times, 1)
				}
			}
			close(done)
		}()
		return done
	}
	wait := func(t *testing.T, done <-chan error) {
		t.Helper()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	// Initial fill.
	for i := 0; i < max; i++ {
		m.Set(uintptr(i), i)
	}
	t.Run("set_get", func(t *testing.T) {
		doneSet := do(t, max, dur, func(t *testing.T, i int) {
			m.Set(uintptr(i), i)
		})

		doneGetHashedKey := do(t, max, dur, func(t *testing.T, i int) {
			if _, ok := m.Get(uintptr(i)); !ok {
				t.Errorf("missing value for key: %d", i)
			}
		})
		wait(t, doneSet)
		wait(t, doneGetHashedKey)
	})
	t.Run("get-or-insert-and-delete", func(t *testing.T) {
		doneGetOrInsert := do(t, max, dur, func(t *testing.T, i int) {
			m.GetOrInsert(uintptr(i), i)
		})
		doneDel := do(t, max, dur, func(t *testing.T, i int) {
			m.Del(uintptr(i))
		})
		wait(t, doneGetOrInsert)
		wait(t, doneDel)
	})
}

func TestHashMap_SetConcurrent(t *testing.T) {
	blocks := &HashMap{}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {

		wg.Add(1)
		go func(blocks *HashMap, i int) {
			defer wg.Done()

			blocks.Set(uintptr(i), struct{}{})

			wg.Add(1)
			go func(blocks *HashMap, i int) {
				defer wg.Done()

				blocks.Get(uintptr(i))
			}(blocks, i)
		}(blocks, i)
	}

	wg.Wait()
}
