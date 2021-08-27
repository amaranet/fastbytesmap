package fastintmap

import (
	"bytes"
	"fmt"
	"github.com/itsabgr/atomic2"
	"reflect"
	"strconv"
	"sync/atomic"
	"unsafe"
)

// DefaultSize is the default size for a zero allocated map
const DefaultSize = 8

// MaxFillRate is the maximum fill rate for the slice before a resize  will happen.
const MaxFillRate = 50

type (
	hashMapData struct {
		keyshifts uintptr        // Pointer size - log2 of array size, to be used as index in the data array
		count     uintptr        // count of filled elements in the slice
		data      unsafe.Pointer // pointer to slice data array
		index     []*ListElement // storage for the slice for the garbage collector to not clean it up
	}

	// HashMap implements a read optimized hash map.
	HashMap struct {
		datamap    unsafe.Pointer // pointer to a map instance that gets replaced if the map resizes
		linkedlist unsafe.Pointer // key sorted linked list of elements
		resizing   uintptr        // flag that marks a resizing operation in progress
	}

	// KeyValue represents a key/value that is returned by the iterator.
	KeyValue struct {
		Key   interface{}
		Value interface{}
	}
)

// New returns a new HashMap instance with a specific initialization size.
func New(size uintptr) *HashMap {
	m := &HashMap{}
	m.allocate(size)
	return m
}

// Len returns the number of elements within the map.
func (m *HashMap) Len() int {
	list := m.list()
	return list.Len()
}

func (m *HashMap) mapData() *hashMapData {
	return (*hashMapData)(atomic.LoadPointer(&m.datamap))
}

func (m *HashMap) list() *List {
	return (*List)(atomic.LoadPointer(&m.linkedlist))
}

func (m *HashMap) allocate(newSize uintptr) {
	list := NewList()
	// atomic swap in case of another allocation happening concurrently
	if atomic.CompareAndSwapPointer(&m.linkedlist, nil, unsafe.Pointer(list)) {
		if atomic.CompareAndSwapUintptr(&m.resizing, uintptr(0), uintptr(1)) {
			m.grow(newSize, false)
		}
	}
}

// FillRate returns the fill rate of the map.
func (m *HashMap) FillRate() float32 {
	data := m.mapData()
	count := float32(atomic.LoadUintptr(&data.count))
	l := float32(uintptr(len(data.index)))
	return count / l
}

func (m *HashMap) resizeNeeded(data *hashMapData, count uintptr) bool {
	l := uintptr(len(data.index))
	if l == 0 {
		return false
	}
	fillRate := (count * 100) / l
	return fillRate > MaxFillRate
}

func (m *HashMap) indexElement(hashedKey uintptr) (data *hashMapData, item *ListElement) {
	data = m.mapData()
	if data == nil {
		return nil, nil
	}
	index := hashedKey >> data.keyshifts
	ptr := (*unsafe.Pointer)(unsafe.Pointer(uintptr(data.data) + index*intSizeBytes))
	item = (*ListElement)(atomic.LoadPointer(ptr))
	return data, item
}

/* The Golang 1.10.1 compiler dons not inline this function well
func (m *HashMap) searchItem(item *ListElement, key interface{}, keyHash uintptr) (value interface{}, ok bool) {
	for item != nil {
		if item.keyHash == keyHash && item.key == key {
			return item.Value(), true
		}

		if item.keyHash > keyHash {
			return nil, false
		}

		item = item.Next()
	}
	return nil, false
}
*/

// Del deletes the key from the map.
func (m *HashMap) Del(key uintptr) {
	list := m.list()
	if list == nil {
		return
	}
	h := key

	var element *ListElement
ElementLoop:
	for _, element = m.indexElement(h); element != nil; element = element.Next() {
		if uintptr(element.key) == h {
			if uintptr(element.key) == key {
				break ElementLoop
			}
		}

		if uintptr(element.key) > h {
			return
		}
	}
	if element == nil {
		return
	}

	m.deleteElement(element)
	list.Delete(element)
}

// DelHashedKey deletes the hashed key from the map.
func (m *HashMap) DelHashedKey(hashedKey uintptr) {
	list := m.list()
	if list == nil {
		return
	}

	// inline HashMap.searchItem()
	var element *ListElement
ElementLoop:
	for _, element = m.indexElement(hashedKey); element != nil; element = element.Next() {
		if uintptr(element.key) == hashedKey {

			break ElementLoop

		}

		if uintptr(element.key) > hashedKey {
			return
		}
	}

	if element == nil {
		return
	}
	m.deleteElement(element)
	list.Delete(element)
}

// deleteElement deletes an element from index
func (m *HashMap) deleteElement(element *ListElement) {
	for {
		data := m.mapData()
		index := element.key >> data.keyshifts
		ptr := (*unsafe.Pointer)(unsafe.Pointer(uintptr(data.data) + uintptr(index*intSizeBytes)))

		next := element.Next()
		if next != nil && element.key>>data.keyshifts != index {
			next = nil // do not set index to next item if it's not the same slice index
		}
		atomic.CompareAndSwapPointer(ptr, unsafe.Pointer(element), unsafe.Pointer(next))

		currentdata := m.mapData()
		if data == currentdata { // check that no resize happened
			break
		}
	}
}

// Insert sets the value under the specified key to the map if it does not exist yet.
// If a resizing operation is happening concurrently while calling Set, the item might show up in the map only after the resize operation is finished.
// Returns true if the item was inserted or false if it existed.
func (m *HashMap) Insert(key uintptr, value interface{}) bool {
	element := &ListElement{
		key:   atomic2.Uintptr(key),
		value: unsafe.Pointer(&value),
	}
	return m.insertListElement(element, false)
}

// Set sets the value under the specified key to the map. An existing item for this key will be overwritten.
// If a resizing operation is happening concurrently while calling Set, the item might show up in the map only after the resize operation is finished.
func (m *HashMap) Set(key uintptr, value interface{}) {

	element := &ListElement{
		key:   atomic2.Uintptr(key),
		value: unsafe.Pointer(&value),
	}
	m.insertListElement(element, true)
}

func (m *HashMap) insertListElement(element *ListElement, update bool) bool {
	for {
		data, existing := m.indexElement(uintptr(element.key))
		if data == nil {
			m.allocate(DefaultSize)
			continue // read mapdata and slice item again
		}
		list := m.list()

		if update {
			if !list.AddOrUpdate(element, existing) {
				continue // a concurrent add did interfere, try again
			}
		} else {
			existed, inserted := list.Add(element, existing)
			if existed {
				return false
			}
			if !inserted {
				continue
			}
		}

		count := data.addItemToIndex(element)
		if m.resizeNeeded(data, count) {
			if atomic.CompareAndSwapUintptr(&m.resizing, uintptr(0), uintptr(1)) {
				go m.grow(0, true)
			}
		}
		return true
	}
}

// CasHashedKey performs a compare and swap operation sets the value under the specified hash key to the map. An existing item for this key will be overwritten.
func (m *HashMap) CasHashedKey(hashedKey uintptr, from, to interface{}) bool {
	data, existing := m.indexElement(hashedKey)
	if data == nil {
		return false
	}
	list := m.list()
	if list == nil {
		return false
	}

	element := &ListElement{
		key:   atomic2.Uintptr(hashedKey),
		value: unsafe.Pointer(&to),
	}
	return list.Cas(element, from, existing)
}

// Cas performs a compare and swap operation sets the value under the specified hash key to the map. An existing item for this key will be overwritten.
func (m *HashMap) Cas(key uintptr, from, to interface{}) bool {
	return m.CasHashedKey(key, from, to)
}

// adds an item to the index if needed and returns the new item counter if it changed, otherwise 0
func (mapData *hashMapData) addItemToIndex(item *ListElement) uintptr {
	index := item.key >> mapData.keyshifts
	ptr := (*unsafe.Pointer)(unsafe.Pointer(uintptr(mapData.data) + uintptr(index*intSizeBytes)))

	for { // loop until the smallest key hash is in the index
		element := (*ListElement)(atomic.LoadPointer(ptr)) // get the current item in the index
		if element == nil {                                // no item yet at this index
			if atomic.CompareAndSwapPointer(ptr, nil, unsafe.Pointer(item)) {
				return atomic.AddUintptr(&mapData.count, 1)
			}
			continue // a new item was inserted concurrently, retry
		}

		if item.key < element.key {
			// the new item is the smallest for this index?
			if !atomic.CompareAndSwapPointer(ptr, unsafe.Pointer(element), unsafe.Pointer(item)) {
				continue // a new item was inserted concurrently, retry
			}
		}
		return 0
	}
}

// Grow resizes the hashmap to a new size, gets rounded up to next power of 2.
// To double the size of the hashmap use newSize 0.
// This function returns immediately, the resize operation is done in a goroutine.
// No resizing is done in case of another resize operation already being in progress.
func (m *HashMap) Grow(newSize uintptr) {
	if atomic.CompareAndSwapUintptr(&m.resizing, uintptr(0), uintptr(1)) {
		go m.grow(newSize, true)
	}
}

func (m *HashMap) grow(newSize uintptr, loop bool) {
	defer atomic.CompareAndSwapUintptr(&m.resizing, uintptr(1), uintptr(0))

	for {
		data := m.mapData()
		if newSize == 0 {
			newSize = uintptr(len(data.index)) << 1
		} else {
			newSize = roundUpPower2(newSize)
		}

		index := make([]*ListElement, newSize)
		header := (*reflect.SliceHeader)(unsafe.Pointer(&index))

		newdata := &hashMapData{
			keyshifts: strconv.IntSize - log2(newSize),
			data:      unsafe.Pointer(header.Data), // use address of slice data storage
			index:     index,
		}

		m.fillIndexItems(newdata) // initialize new index slice with longer keys

		atomic.StorePointer(&m.datamap, unsafe.Pointer(newdata))

		m.fillIndexItems(newdata) // make sure that the new index is up to date with the current state of the linked list

		if !loop {
			break
		}

		// check if a new resize needs to be done already
		count := uintptr(m.Len())
		if !m.resizeNeeded(newdata, count) {
			break
		}
		newSize = 0 // 0 means double the current size
	}
}

func (m *HashMap) fillIndexItems(mapData *hashMapData) {
	list := m.list()
	if list == nil {
		return
	}
	first := list.First()
	item := first
	lastIndex := uintptr(0)

	for item != nil {
		index := uintptr(item.key >> mapData.keyshifts)
		if item == first || index != lastIndex { // store item with smallest hash key for every index
			mapData.addItemToIndex(item)
			lastIndex = index
		}
		item = item.Next()
	}
}

// String returns the map as a string, only hashed keys are printed.
func (m *HashMap) String() string {
	list := m.list()
	if list == nil {
		return "[]"
	}

	buffer := bytes.NewBufferString("")
	buffer.WriteRune('[')

	first := list.First()
	item := first

	for item != nil {
		if item != first {
			buffer.WriteRune(',')
		}
		fmt.Fprint(buffer, item.key)
		item = item.Next()
	}
	buffer.WriteRune(']')
	return buffer.String()
}

// Iter returns an iterator which could be used in a for range loop.
// The order of the items is sorted by hash keys.
func (m *HashMap) Iter() <-chan KeyValue {
	ch := make(chan KeyValue) // do not use a size here since items can get added during iteration

	go func() {
		list := m.list()
		if list == nil {
			close(ch)
			return
		}
		item := list.First()
		for item != nil {
			value := item.Value()
			ch <- KeyValue{item.key, value}
			item = item.Next()
		}
		close(ch)
	}()

	return ch
}
