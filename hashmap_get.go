package fastintmap

import (
	"github.com/itsabgr/atomic2"
	"unsafe"
)

// Get retrieves an element from the map under given hashed key.
func (m *HashMap) Get(hashedKey uintptr) (value interface{}, ok bool) {
	data, element := m.indexElement(hashedKey)
	if data == nil {
		return nil, false
	}

	// inline HashMap.searchItem()
	for element != nil {
		if uintptr(element.key) == hashedKey {
			return element.Value(), true
		}

		if uintptr(element.key) > hashedKey {
			return nil, false
		}

		element = element.Next()
	}
	return nil, false
}

// GetOrInsert returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
func (m *HashMap) GetOrInsert(key uintptr, value interface{}) (actual interface{}, loaded bool) {
	h := key
	var newelement *ListElement

	for {
		data, element := m.indexElement(h)
		if data == nil {
			m.allocate(DefaultSize)
			continue
		}

		for element != nil {
			if uintptr(element.key) == h {

				if uintptr(element.key) == key {
					actual = element.Value()
					return actual, true

				}
			}

			if uintptr(element.key) > h {
				break
			}

			element = element.Next()
		}

		if newelement == nil { // allocate only once
			newelement = &ListElement{
				key:   atomic2.Uintptr(key),
				value: unsafe.Pointer(&value),
			}
		}

		if m.insertListElement(newelement, false) {
			return value, false
		}
	}
}
