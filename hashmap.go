package fastintmap

import (
	"bytes"
	"fmt"
	"github.com/itsabgr/fastintmap/pkg/sortedlist"
	"github.com/itsabgr/go-handy"
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
		keyShifts uintptr                   // Pointer size - log2 of array size, to be used as index in the data array
		count     uintptr                   // count of filled elements in the slice
		data      unsafe.Pointer            // pointer to slice data array
		index     []*sortedlist.ListElement // storage for the slice for the garbage collector to not clean it up
	}

	// Map implements a read optimized hash map.
	Map struct {
		_noCopy  handy.NoCopy
		dataMap  unsafe.Pointer // pointer to a map instance that gets replaced if the map resizes
		listPtr  unsafe.Pointer // key sorted linked list of elements
		resizing uintptr        // flag that marks a resizing operation in progress
	}

	// KeyValue represents a key/value that is returned by the iterator.
	KeyValue struct {
		Key   uintptr
		Value interface{}
	}
)

// New returns a new Map instance with a specific initialization size.
func New(size uintptr) *Map {
	m := &Map{}
	m.allocate(size)
	return m
}

// Len returns the number of elements within the map.
func (m *Map) Len() int {
	list := m.list()
	return list.Len()
}

func (m *Map) mapData() *hashMapData {
	return (*hashMapData)(atomic.LoadPointer(&m.dataMap))
}

func (m *Map) list() *sortedlist.List {
	return (*sortedlist.List)(atomic.LoadPointer(&m.listPtr))
}

func (m *Map) allocate(newSize uintptr) {
	list := sortedlist.New()
	// atomic swap in case of another allocation happening concurrently
	if atomic.CompareAndSwapPointer(&m.listPtr, nil, unsafe.Pointer(list)) {
		if atomic.CompareAndSwapUintptr(&m.resizing, uintptr(0), uintptr(1)) {
			m.grow(newSize, false)
		}
	}
}

// FillRate returns the fill rate of the map.
func (m *Map) FillRate() float32 {
	data := m.mapData()
	count := float32(atomic.LoadUintptr(&data.count))
	l := float32(uintptr(len(data.index)))
	return count / l
}

func (m *Map) resizeNeeded(data *hashMapData, count uintptr) bool {
	l := uintptr(len(data.index))
	if l == 0 {
		return false
	}
	fillRate := (count * 100) / l
	return fillRate > MaxFillRate
}

func (m *Map) indexElement(hashedKey uintptr) (data *hashMapData, item *sortedlist.ListElement) {
	data = m.mapData()
	if data == nil {
		return nil, nil
	}
	index := hashedKey >> data.keyShifts
	ptr := (*unsafe.Pointer)(unsafe.Pointer(uintptr(data.data) + index*intSizeBytes))
	item = (*sortedlist.ListElement)(atomic.LoadPointer(ptr))
	return data, item
}

/* The Golang 1.10.1 compiler dons not inline this function well
func (m *Map) searchItem(item *ListElement, key interface{}, keyHash uintptr) (value interface{}, ok bool) {
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

// Delete deletes the hashed key from the map.
func (m *Map) Delete(hashedKey uintptr) {
	list := m.list()
	if list == nil {
		return
	}

	// inline Map.searchItem()
	var element *sortedlist.ListElement
ElementLoop:
	for _, element = m.indexElement(hashedKey); element != nil; element = element.Next() {
		if element.Key() == hashedKey {

			break ElementLoop

		}

		if element.Key() > hashedKey {
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
func (m *Map) deleteElement(element *sortedlist.ListElement) {
	for {
		data := m.mapData()
		index := element.Key() >> data.keyShifts
		ptr := (*unsafe.Pointer)(unsafe.Pointer(uintptr(data.data) + index*intSizeBytes))

		next := element.Next()
		if next != nil && element.Key()>>data.keyShifts != index {
			next = nil // do not set index to next item if it's not the same slice index
		}
		atomic.CompareAndSwapPointer(ptr, unsafe.Pointer(element), unsafe.Pointer(next))

		currentData := m.mapData()
		if data == currentData { // check that no resize happened
			break
		}
	}
}

// Add sets the value under the specified key to the map if it does not exist yet.
// If a resizing operation is happening concurrently while calling Set, the item might show up in the map only after the resize operation is finished.
// Returns true if the item was inserted or false if it existed.
func (m *Map) Add(key uintptr, value interface{}) bool {
	element := sortedlist.NewElement(key, value)
	return m.insertListElement(element, false)
}

// Set sets the value under the specified key to the map. An existing item for this key will be overwritten.
// If a resizing operation is happening concurrently while calling Set, the item might show up in the map only after the resize operation is finished.
func (m *Map) Set(key uintptr, value interface{}) {

	element := sortedlist.NewElement(key, value)
	m.insertListElement(element, true)
}

func (m *Map) insertListElement(element *sortedlist.ListElement, update bool) bool {
	for {
		data, existing := m.indexElement(element.Key())
		if data == nil {
			m.allocate(DefaultSize)
			continue // read mapData and slice item again
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

// CAS performs a compare and swap operation sets the value under the specified hash key to the map. An existing item for this key will be overwritten.
func (m *Map) CAS(hashedKey uintptr, from, to interface{}) bool {
	data, existing := m.indexElement(hashedKey)
	if data == nil {
		return false
	}
	list := m.list()
	if list == nil {
		return false
	}

	element := sortedlist.NewElement(hashedKey, to)
	return list.Cas(element, from, existing)
}

// adds an item to the index if needed and returns the new item counter if it changed, otherwise 0
func (mapData *hashMapData) addItemToIndex(item *sortedlist.ListElement) uintptr {
	index := item.Key() >> mapData.keyShifts
	ptr := (*unsafe.Pointer)(unsafe.Pointer(uintptr(mapData.data) + index*intSizeBytes))

	for { // loop until the smallest key hash is in the index
		element := (*sortedlist.ListElement)(atomic.LoadPointer(ptr)) // get the current item in the index
		if element == nil {                                           // no item yet at this index
			if atomic.CompareAndSwapPointer(ptr, nil, unsafe.Pointer(item)) {
				return atomic.AddUintptr(&mapData.count, 1)
			}
			continue // a new item was inserted concurrently, retry
		}

		if item.Key() < element.Key() {
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
func (m *Map) Grow(newSize uintptr) {
	if atomic.CompareAndSwapUintptr(&m.resizing, uintptr(0), uintptr(1)) {
		go m.grow(newSize, true)
	}
}

func (m *Map) grow(newSize uintptr, loop bool) {
	defer atomic.CompareAndSwapUintptr(&m.resizing, uintptr(1), uintptr(0))

	for {
		data := m.mapData()
		if newSize == 0 {
			newSize = uintptr(len(data.index)) << 1
		} else {
			newSize = roundUpPower2(newSize)
		}

		index := make([]*sortedlist.ListElement, newSize)
		header := (*reflect.SliceHeader)(unsafe.Pointer(&index))

		newData := &hashMapData{
			keyShifts: strconv.IntSize - log2(newSize),
			data:      unsafe.Pointer(header.Data), // use address of slice data storage
			index:     index,
		}

		m.fillIndexItems(newData) // initialize new index slice with longer keys

		atomic.StorePointer(&m.dataMap, unsafe.Pointer(newData))

		m.fillIndexItems(newData) // make sure that the new index is up to date with the current state of the linked list

		if !loop {
			break
		}

		// check if a new resize needs to be done already
		count := uintptr(m.Len())
		if !m.resizeNeeded(newData, count) {
			break
		}
		newSize = 0 // 0 means double the current size
	}
}

func (m *Map) fillIndexItems(mapData *hashMapData) {
	list := m.list()
	if list == nil {
		return
	}
	first := list.First()
	item := first
	lastIndex := uintptr(0)

	for item != nil {
		index := item.Key() >> mapData.keyShifts
		if item == first || index != lastIndex { // store item with smallest hash key for every index
			mapData.addItemToIndex(item)
			lastIndex = index
		}
		item = item.Next()
	}
}

// String returns the map as a string, only hashed keys are printed.
func (m *Map) String() string {
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
		_, _ = fmt.Fprint(buffer, item.Key())
		item = item.Next()
	}
	buffer.WriteRune(']')
	return buffer.String()
}

//Visit visits the entries in key order, calling fn for each. if the fn returns non-nil error stops process and returns that error
func (m *Map) Visit(fn func(key uintptr, value interface{}) error) error {
	list := m.list()
	if list == nil {
		return nil
	}
	item := list.First()
	for item != nil {
		value := item.Value()
		err := fn(item.Key(), value)
		if err != nil {
			return err
		}
		item = item.Next()
	}
	return nil
}
